package sous

import (
	"fmt"

	"github.com/opentable/singularity/dtos"
	"github.com/opentable/sous/util/docker_registry"
	"github.com/samsalisbury/semv"
)

type (
	deploymentBuilder struct {
		target         Deployment
		depMarker      sDepMarker
		deploy         sDeploy
		request        sRequest
		req            singReq
		registryClient *docker_registry.Client
	}

	canRetryRequest struct {
		cause error
		req   singReq
	}
)

func (cr *canRetryRequest) Error() string {
	return fmt.Sprintf("%s: %s", cr.cause, cr.name())
}

func (cr *canRetryRequest) name() string {
	return fmt.Sprintf("%s:%s", cr.req.sourceUrl, cr.req.reqParent.Request.Id)
}

func newDeploymentBuilder(cl *docker_registry.Client, req singReq) deploymentBuilder {
	return deploymentBuilder{registryClient: cl, req: req}
}

func (uc *deploymentBuilder) canRetry(err error) error {
	if uc.req.sourceUrl != "" &&
		uc.req.reqParent != nil &&
		uc.req.reqParent.Request != nil &&
		uc.req.reqParent.Request.Id != "" {
		return &canRetryRequest{err, uc.req}
	} else {
		return err
	}
}

func (uc *deploymentBuilder) completeConstruction() error {
	uc.target.Cluster = uc.req.sourceUrl
	uc.request = uc.req.reqParent.Request

	err := uc.retrieveDeploy()
	if err != nil {
		return uc.canRetry(err)
	}

	err = uc.retrieveImageLabels()
	if err != nil {
		return uc.canRetry(err)
	}

	err = uc.unpackDeployConfig()
	if err != nil {
		return uc.canRetry(err)
	}

	err = uc.determineManifestKind()
	if err != nil {
		return uc.canRetry(err)
	}

	return nil
}

func (uc *deploymentBuilder) retrieveDeploy() error {

	rp := uc.req.reqParent
	rds := rp.RequestDeployState
	sing := uc.req.sing

	if rds == nil {
		return fmt.Errorf("Singularity response didn't include a deploy state")
	}
	uc.depMarker = rds.PendingDeploy
	if uc.depMarker == nil {
		uc.depMarker = rds.ActiveDeploy
	}
	if uc.depMarker == nil {
		return fmt.Errorf("Singularity deploy state included no dep markers")
	}

	dh, err := sing.GetDeploy(uc.depMarker.RequestId, uc.depMarker.DeployId) // !!! makes HTTP req
	if err != nil {
		return err
	}

	uc.deploy = dh.Deploy
	if uc.deploy == nil {
		return fmt.Errorf("Singularity deploy history included no deploy")
	}

	return nil
}

func (uc *deploymentBuilder) retrieveImageLabels() error {
	ci := uc.deploy.ContainerInfo
	if ci.Type != dtos.SingularityContainerInfoSingularityContainerTypeDOCKER {
		return fmt.Errorf("Singularity container isn't a docker container")
	}
	dkr := ci.Docker
	if dkr == nil {
		return fmt.Errorf("Singularity deploy didn't include a docker info")
	}

	imageName := dkr.Image

	labels, err := uc.registryClient.LabelsForImageName(imageName) // !!! HTTP request
	if err != nil {
		return err
	}

	uc.target.SourceVersion, err = buildSourceVersion(labels)
	if err != nil {
		return err
	}

	return nil
}

func (uc *deploymentBuilder) unpackDeployConfig() error {
	uc.target.Env = uc.deploy.Env
	if uc.target.Env == nil {
		uc.target.Env = make(map[string]string)
	}

	singRez := uc.deploy.Resources
	uc.target.Resources = make(Resources)
	uc.target.Resources["cpus"] = fmt.Sprintf("%f", singRez.Cpus)
	uc.target.Resources["memory"] = fmt.Sprintf("%f", singRez.MemoryMb)
	uc.target.Resources["ports"] = fmt.Sprintf("%d", singRez.NumPorts)

	uc.target.NumInstances = int(uc.request.Instances)
	uc.target.Owners = uc.request.Owners

	return nil
}

func (uc *deploymentBuilder) determineManifestKind() error {
	switch uc.request.RequestType {
	default:
		return fmt.Errorf("Unrecognized response tupe returned by Singularlity: %v", uc.request.RequestType)
	case dtos.SingularityRequestRequestTypeSERVICE:
		uc.target.Kind = ManifestKindService
	case dtos.SingularityRequestRequestTypeWORKER:
		uc.target.Kind = ManifestKindWorker
	case dtos.SingularityRequestRequestTypeON_DEMAND:
		uc.target.Kind = ManifestKindOnDemand
	case dtos.SingularityRequestRequestTypeSCHEDULED:
		uc.target.Kind = ManifestKindScheduled
	case dtos.SingularityRequestRequestTypeRUN_ONCE:
		uc.target.Kind = ManifestKindOnce
	}
	return nil
}

func buildSourceVersion(labels map[string]string) (SourceVersion, error) {
	missingLabels := make([]string, 0, 3)
	repo, present := labels[DockerRepoLabel]
	if !present {
		missingLabels = append(missingLabels, DockerRepoLabel)
	}

	versionStr, present := labels[DockerVersionLabel]
	if !present {
		missingLabels = append(missingLabels, DockerVersionLabel)
	}

	revision, present := labels[DockerRevisionLabel]
	if !present {
		missingLabels = append(missingLabels, DockerRevisionLabel)
	}

	path, present := labels[DockerPathLabel]
	if !present {
		missingLabels = append(missingLabels, DockerPathLabel)
	}

	if len(missingLabels) > 0 {
		err := fmt.Errorf("Missing labels on manifest for %v", missingLabels)
		return SourceVersion{}, err
	}

	version, err := semv.Parse(versionStr)
	version.Meta = revision

	return SourceVersion{
		RepoURL:    RepoURL(repo),
		Version:    version,
		RepoOffset: RepoOffset(path),
	}, err
}