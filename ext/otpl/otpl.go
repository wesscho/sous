// Package otpl adds some OpenTable-specific interop methods. These will one day
// be removed.
package otpl

import (
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"
	"sync"

	"strings"

	"github.com/opentable/sous/lib"
	"github.com/opentable/sous/util/logging"
	"github.com/opentable/sous/util/logging/messages"
	"github.com/opentable/sous/util/shell"
)

type (
	// ManifestParser parses sous.DeploySpecs from otpl-deploy config files.
	// NOTE: otpl-deploy config is an internal tool at OpenTable, one day this
	// code will be removed.
	ManifestParser struct {
		Log logging.LogSink
		WD  shell.Shell
	}
	// SingularityJSON represents the JSON in an otpl-deploy singularity.json
	// file. Note that the json tags are essential to validating parsed JSON
	// contains only recognised fields.
	SingularityJSON struct {
		Resources SingularityResources `json:"resources,omitempty"`
		Env       sous.Env             `json:"env,omitempty"`
	}
	// SingularityResources represents the resources section in SingularityJSON.
	SingularityResources map[string]float64
	// SingularityRequestJSON represents JSON in an otpl-deploy
	// singularity-request.json file.
	SingularityRequestJSON struct {
		// Instances is the number of instances in this deployment.
		Instances int `json:"instances,omitempty"`
		// Owners is a comma-separated list of email addresses.
		Owners []string `json:"owners,omitempty"`
		// NOTE: We do not currently support Daemon, RackSensitive or LoadBalanced
		//Daemon, RackSensitive, LoadBalanced bool
	}
)

// SousResources returns the equivalent sous.Resources.
func (sr SingularityResources) SousResources() sous.Resources {
	r := make(sous.Resources, len(sr))
	for k, v := range sr {
		sousName, ok := resourceNameSingToSous[k]
		if !ok {
			sousName = k
		}
		r[sousName] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	return r
}

var resourceNameSingToSous = map[string]string{
	"cpus":     "cpus",
	"numPorts": "ports",
	"memoryMb": "memory",
}

// NewManifestParser generates a new ManifestParser with default logging.
func NewManifestParser(ls logging.LogSink) *ManifestParser {
	return &ManifestParser{Log: ls}
}

type otplDeployConfig struct {
	// Name is "<cluster>".
	// It is unique for all OTPL configs in a single project by flavor.
	Name   string
	Owners []string
	Spec   *sous.DeploySpec
}

type otplDeployManifest struct {
	Owners sous.OwnerSet
	Specs  sous.DeploySpecs
}

type otplDeployManifests map[string]otplDeployManifest

func getDeployManifest(manifests otplDeployManifests, key string) otplDeployManifest {
	if manifest, ok := manifests[key]; ok {
		return manifest
	}
	manifest := otplDeployManifest{
		Owners: sous.OwnerSet{},
		Specs:  sous.DeploySpecs{},
	}
	manifests[key] = manifest
	return manifest
}

// ParseManifests searches the working directory of wd to find otpl-deploy
// config files in their standard locations (config/{cluster-name}] or
// config/{cluster-name}.{flavor}), and converts them to sous.DeploySpecs.
func (mp *ManifestParser) ParseManifests(wd shell.Shell) sous.Manifests {
	wd = wd.Clone()
	manifests := sous.NewManifests()
	if err := wd.CD("config"); err != nil {
		return manifests
	}
	l, err := wd.List()
	if err != nil {
		messages.ReportLogFieldsMessageToConsole("error list of clone", logging.WarningLevel, mp.Log, err)
		return manifests
	}
	c := make(chan *otplDeployConfig)
	wg := sync.WaitGroup{}
	wg.Add(len(l))
	go func() { wg.Wait(); close(c) }()
	for _, f := range l {
		f := f
		go func() {
			defer wg.Done()
			if !f.IsDir() {
				return
			}
			wd := wd.Clone()
			if err := wd.CD(f.Name()); err != nil {
				messages.ReportLogFieldsMessageToConsole("error cloning", logging.WarningLevel, mp.Log, err)
				return
			}
			if otplConfig := mp.parseSingleOTPLConfig(wd); otplConfig != nil {
				c <- otplConfig
			}
		}()
	}
	deployManifests := otplDeployManifests{}
	for s := range c {
		cluster, flavor := getClusterAndFlavor(s)
		deployManifest := getDeployManifest(deployManifests, flavor)
		deployManifest.Specs[cluster] = *s.Spec
		for _, o := range s.Owners {
			deployManifest.Owners.Add(o)
		}
	}

	for flavor, dm := range deployManifests {
		manifests.Add(&sous.Manifest{
			Flavor:      flavor,
			Deployments: dm.Specs,
			Owners:      dm.Owners.Slice(),
		})
	}
	return manifests
}

// GetClusterAndFlavor returns the cluster and flavor by extracting values
// from the otplDeployConfig name.  The pattern is {cluster}.{flavor} as
// defined in the otpl scripts.
func getClusterAndFlavor(s *otplDeployConfig) (string, string) {
	splitName := strings.Split(s.Name, ".")
	cluster := splitName[0]
	flavor := ""
	if len(splitName) > 1 {
		flavor = splitName[1]
	}
	return cluster, flavor
}

// ParseSingleOTPLConfig returns a single sous.DeploySpec from the working
// directory of wd. It assumes that this directory contains at least a file
// called singularity.json, and optionally an additional file called
// singularity-requst.json.
func (mp *ManifestParser) parseSingleOTPLConfig(wd shell.Shell) *otplDeployConfig {
	if !wd.Exists("singularity.json") {
		messages.ReportLogFieldsMessageToConsole("no singularity.json present", logging.WarningLevel, mp.Log, wd.Dir())
		return nil
	}
	rawJSON, err := wd.Stdout("cat", "singularity.json")
	if err != nil {
		messages.ReportLogFieldsMessageToConsole("error reading path", logging.WarningLevel, mp.Log, path.Join(wd.Dir(), "singularity.json"), err)
		return nil
	}

	v, err := parseSingularityJSON(rawJSON)
	if err != nil {
		m := fmt.Sprintf("error parsing singularity.json: %s", err)
		messages.ReportLogFieldsMessageToConsole(m, logging.WarningLevel, mp.Log, path.Join(wd.Dir(), "singularity.json"), err)
		return nil
	}

	if v.Env == nil {
		v.Env = map[string](string){}
	}

	deploySpec := &otplDeployConfig{
		Name: path.Base(wd.Dir()),
		Spec: &sous.DeploySpec{
			DeployConfig: sous.DeployConfig{
				Resources: v.Resources.SousResources(),
				Env:       v.Env,
			},
		},
	}
	if !wd.Exists("singularity-request.json") {
		messages.ReportLogFieldsMessageToConsole("no singularity-request.json", logging.WarningLevel, mp.Log, wd.Dir())
		return deploySpec
	}
	rawSRJSON, err := wd.Stdout("cat", "singularity-request.json")
	if err != nil {
		messages.ReportLogFieldsMessageToConsole("failed to read singularity-request.json: "+err.Error(), logging.WarningLevel, mp.Log, err)
		return deploySpec
	}

	request, err := parseSingularityRequestJSON(rawSRJSON)
	if err != nil {
		m := fmt.Sprintf("error parsing singularity-request.json: %s", err)
		messages.ReportLogFieldsMessageToConsole(m, logging.WarningLevel, mp.Log, path.Join(wd.Dir(), "singularity-request.json"), err)
		return nil
	}

	deploySpec.Spec.NumInstances = request.Instances
	deploySpec.Owners = request.Owners
	return deploySpec
}

func strictParseJSON(rawJSON string, v interface{}) error {
	comp := map[string]interface{}{}
	if err := json.Unmarshal([]byte(rawJSON), v); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(rawJSON), &comp); err != nil {
		return err
	}
	compJSONb, err := json.Marshal(comp)
	if err != nil {
		return err
	}
	understoodJSONb, err := json.Marshal(v)
	if err != nil {
		return err
	}
	understoodJSON := string(understoodJSONb)
	compJSON := string(compJSONb)

	equal, err := equalJSON(compJSON, understoodJSON)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("unrecognised fields:\n%sunderstood:\n%s",
			compJSON, understoodJSON)
	}
	return nil
}

func equalJSON(a, b string) (bool, error) {
	var aVal, bVal interface{}
	if err := json.Unmarshal([]byte(a), &aVal); err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(b), &bVal); err != nil {
		return false, err
	}
	return reflect.DeepEqual(aVal, bVal), nil
}

func validateResources(v SingularityJSON) error {
	seen := map[string]struct{}{}
	for k := range v.Resources {
		if _, ok := resourceNameSingToSous[k]; !ok {
			return fmt.Errorf("invalid resource name %q", k)
		}
		seen[k] = struct{}{}
	}
	var missing []string
	for k := range resourceNameSingToSous {
		if _, ok := seen[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing resource(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func parseSingularityJSON(rawJSON string) (SingularityJSON, error) {
	v := SingularityJSON{}
	if err := strictParseJSON(rawJSON, &v); err != nil {
		return v, err
	}
	if err := validateResources(v); err != nil {
		return v, err
	}
	return v, nil
}

func parseSingularityRequestJSON(rawJSON string) (SingularityRequestJSON, error) {
	v := SingularityRequestJSON{}
	err := strictParseJSON(rawJSON, &v)
	return v, err
}
