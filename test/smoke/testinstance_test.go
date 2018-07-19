//+build smoke

package smoke

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/opentable/sous/config"
	"github.com/opentable/sous/ext/storage"
	sous "github.com/opentable/sous/lib"
	"github.com/opentable/sous/util/filemap"
	"github.com/opentable/sous/util/logging"
	"github.com/opentable/sous/util/yaml"
)

type Instance struct {
	Bin
	Addr                string
	StateDir, ConfigDir string
	ClusterName         string
	Proc                *os.Process
	LogDir              string
	// Num is the instance number for display purposes.
	Num int
}

func makeInstance(t *testing.T, binPath string, i int, clusterName, baseDir, addr string, finished <-chan struct{}) (*Instance, error) {
	baseDir = path.Join(baseDir, fmt.Sprintf("instance%d", i+1))
	stateDir := path.Join(baseDir, "state")

	name := fmt.Sprintf("instance%d_%s", i, clusterName)

	bin := NewBin(binPath, name, baseDir, finished)
	bin.Env["SOUS_CONFIG_DIR"] = bin.ConfigDir

	return &Instance{
		Bin:         bin,
		Addr:        addr,
		ClusterName: clusterName,
		StateDir:    stateDir,
		Num:         i + 1,
	}, nil
}

func seedDB(config *config.Config, state *sous.State) error {
	db, err := config.Database.DB()
	if err != nil {
		return err
	}
	mgr := storage.NewPostgresStateManager(db, logging.SilentLogSet())

	return mgr.WriteState(state, sous.User{})
}

func (i *Instance) Configure(config *config.Config, remoteGDMDir string, fcfg fixtureConfig) error {
	if err := seedDB(config, fcfg.startState); err != nil {
		return err
	}

	if err := os.MkdirAll(i.StateDir, 0777); err != nil {
		return err
	}

	configYAML, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	i.Bin.Configure(filemap.FileMap{
		"config.yaml": string(configYAML),
	})

	gdmDir := i.StateDir
	if err := doCMD(gdmDir+"/..", "git", "clone", remoteGDMDir, gdmDir); err != nil {
		return err
	}
	username := fmt.Sprintf("Sous Server %s", i.ClusterName)
	if err := doCMD(gdmDir, "git", "config", "user.name", username); err != nil {
		return err
	}
	email := fmt.Sprintf("sous-%s@example.com", i.ClusterName)
	if err := doCMD(gdmDir, "git", "config", "user.email", email); err != nil {
		return err
	}

	return nil
}

func (i *Instance) Start(t *testing.T) {
	t.Helper()

	if !quiet() {
		fmt.Fprintf(os.Stderr, "==> Instance %q config:\n", i.ClusterName)
	}
	// Run 'sous config' to validate it.
	i.Bin.MustRun(t, "config", nil)

	serverDebug := os.Getenv("SOUS_SERVER_DEBUG") == "true"
	prepared := i.Bin.Command(t, "server", nil, "-listen", i.Addr, "-cluster", i.ClusterName, "autoresolver=false", fmt.Sprintf("-d=%t", serverDebug))

	cmd := prepared.Cmd
	if err := cmd.Start(); err != nil {
		t.Fatalf("error starting server %q: %s", i.Name, err)
	}

	if cmd.Process == nil {
		panic("cmd.Process nil after cmd.Start")
	}

	go func() {
		select {
		// In this case the process ended before the test finished.
		case err := <-func() <-chan error {
			_, err := cmd.Process.Wait()
			c := make(chan error, 1)
			c <- err
			return c
		}():
			rtLog("SERVER CRASHED: %s", err)
			// TODO SS: Dump log tail here as well for analysis.
		// In this case the process is still running.
		case <-i.TestFinished:
			// Do nothing.
		}
	}()

	i.Proc = cmd.Process
	writePID(t, i.Proc.Pid)
}

func (i *Instance) Stop() error {
	if i.Proc == nil {
		return fmt.Errorf("cannot stop instance %q (not started)", i.Num)
	}
	if err := i.Proc.Kill(); err != nil {
		return fmt.Errorf("cannot kill instance %q: %s", i.Num, err)
	}
	return nil
}

const pidFile = "test-server-pids"
