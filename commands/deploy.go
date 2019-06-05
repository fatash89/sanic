package commands

import (
	"bytes"
	"fmt"
	"github.com/distributed-containers-inc/sanic/config"
	"github.com/distributed-containers-inc/sanic/provisioners"
	"github.com/distributed-containers-inc/sanic/shell"
	"github.com/urfave/cli"
	"os/exec"
	"path/filepath"
)

func runTemplater(folderIn, folderOut, templaterImage string) error {
	shl, err := shell.Current()
	if err != nil {
		return err
	}

	provisioner, err := provisioners.GetProvisioner()
	if err != nil {
		return err
	}
	registry, err := provisioner.Registry()
	if err != nil {
		return err
	}

	cmd := exec.Command(
		"docker",
		"run",
		"--rm",
		"-v", folderIn+"/:/in:ro",
		"-v", folderOut+"/:/out",
		"-e", "SANIC_ENV="+shl.GetSanicEnvironment(),
		"-e", "REGISTRY_HOST="+registry,
		templaterImage,
	)
	stderrBuffer := &bytes.Buffer{}
	cmd.Stderr = stderrBuffer
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf(
			"could not generate the kubernetes configurations from %s: %s\n%s",
			folderIn, err.Error(), stderrBuffer.String())
	}
	return nil
}

func kubectlApplyFolder(folder string, provisioner provisioners.Provisioner) error {
	cmd := exec.Command("kubectl","--kubeconfig", provisioner.KubeConfigLocation(), "apply", "-f", folder)
	return cmd.Run()
}

func deployCommandAction(cliContext *cli.Context) error {
	cfg, err := config.Read()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}

	shl, err := shell.Current()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	folderIn, err := filepath.Abs(shl.GetSanicRoot() + "/" + cfg.Deploy.Folder + "/in")
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	folderOut, err := filepath.Abs(shl.GetSanicRoot() + "/" + cfg.Deploy.Folder + "/out")
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}

	provisioner, err := provisioners.GetProvisioner()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	err = provisioner.EnsureCluster()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	err = runTemplater(folderIn, folderOut, cfg.Deploy.TemplaterImage)
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	err = kubectlApplyFolder(folderOut, provisioner)
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	return nil
}

var deployCommand = cli.Command{
	Name:   "deploy",
	Usage:  "deploy some (or all, by default) services",
	Action: deployCommandAction,
}
