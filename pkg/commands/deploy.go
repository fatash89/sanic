package commands

import (
	"bytes"
	"fmt"
	"github.com/webappio/sanic/pkg/bridge/git"
	"github.com/webappio/sanic/pkg/config"
	"github.com/webappio/sanic/pkg/provisioners/minikube"
	"github.com/webappio/sanic/pkg/provisioners/provisioner"
	"github.com/webappio/sanic/pkg/shell"
	"github.com/webappio/sanic/pkg/util"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func getenv(key string, default_ ...string) string {
	env := os.Getenv(key)
	if env != "" {
		return env
	}
	return strings.Join(default_, " ")
}

func clearYamlsFromDir(folderOut string) error {
	files, err := filepath.Glob(folderOut + "/*.yaml")
	if err != nil {
		return err
	}
	for _, f := range files {
		err = os.Remove(f)
		if err != nil {
			return err
		}
	}
	return nil
}

func pullImageIfNotExists(image string) error {
	cmd := exec.Command("docker", "inspect", image)
	if cmd.Run() == nil {
		return nil //already exists
	}
	fmt.Println("Pulling the templater image " + image + "...")
	cmd = exec.Command(
		"docker",
		"pull",
		image,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runTemplater(folderIn, folderOut, templaterImage, namespace string, args cli.Args) error {
	if namespace == "" {
		namespace = "<ERROR_NAMESPACE_NOT_DEFINED_IN_THIS_ENV>"
	}

	cfg, err := config.Read()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	shl, err := shell.Current()
	if err != nil {
		return err
	}
	provisioner, err := getProvisioner()
	if err != nil {
		return err
	}
	registry, _, err := provisioner.Registry()
	if err != nil {
		return err
	}
	services, err := util.FindServices(shl.GetSanicRoot(), cfg.Build.IgnoreDirs)
	if err != nil {
		return err
	}
	var serviceDirectories []string
	for _, service := range services {
		serviceDirectories = append(serviceDirectories, service.Dir)
	}
	buildTag, err := git.GetCurrentTreeHash(shl.GetSanicRoot(), serviceDirectories...)
	if err != nil {
		return err
	}
	err = clearYamlsFromDir(folderOut)
	if err != nil {
		return err
	}

	tempFolderOut, err := ioutil.TempDir("", "sanicdeploy")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempFolderOut)

	if !strings.Contains(templaterImage, ":") {
		templaterImage = templaterImage + ":latest"
	}

	err = pullImageIfNotExists(templaterImage)
	if err != nil {
		return fmt.Errorf("could not pull the templater image %s: %s", templaterImage, err)
	}

	allFiles, err := ioutil.ReadDir(folderIn)
	if err != nil {
		return errors.Wrap(err, "could not read template files at "+folderIn)
	}

	var files []string
	if args.Present() {
		for _, file := range allFiles {
			for _, arg := range args {
				if file.Name() == arg {
					files = append(files, filepath.Join(folderIn, file.Name()))
					break
				}
			}
		}
	} else {
		for _, file := range allFiles {
			if strings.HasSuffix(file.Name(), ".tmpl") {
				files = append(files, filepath.Join(folderIn, file.Name()))
			}
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("No configuration files were found\n")
	}

	fmt.Printf("Templating %d config files (%d total found)...\n", len(files), len(allFiles))

	if shl.GetSanicEnvironment() == "ci" {
		os.Setenv("SANIC_ENV","dev")
	} else {
		os.Setenv("SANIC_ENV",shl.GetSanicEnvironment())
	}
	os.Setenv("REGISTRY_HOST",registry)
	os.Setenv("IMAGE_TAG", buildTag)
	os.Setenv("PROJECT_DIR", provisioner.InClusterDir(shl.GetSanicRoot()))
	os.Setenv("NAMESPACE", namespace)

	for _, templatepath := range files {
		templateName := strings.TrimSuffix(filepath.Base(templatepath), ".tmpl")
		fmt.Printf("Running template %s...\n", templateName)
		t, err := template.New(
			filepath.Base(templatepath),
		).Funcs(
			map[string]interface{}{
				"getenv": getenv,
			},
		).ParseFiles(
			append([]string{templatepath}, files...)...
		)
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(folderOut+"/"+templateName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("Could not open the file at %s for writing. Did you run this image with -v (output path on host):/out ?\n", folderOut+"/"+templateName)
		}
		outFile.WriteString("#WARNING: THIS FILE IS AUTOMATICALLY GENERATED, DO NOT EDIT IT DIRECTLY OR COMMIT IT\n")

		err = t.Execute(outFile, nil)
		outFile.Close()
		if err != nil {
			return fmt.Errorf("could not write the template %s to the directory %s: %s", templatepath, folderOut, err.Error())
		}
	}
	return nil
}

func createNamespace(namespace string, provisioner provisioner.Provisioner) error {
	cmd, err := provisioner.KubectlCommand("create", "namespace", namespace)
	if err != nil {
		return errors.Wrapf(err, "error creating namespace %s", namespace)
	}
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	err = cmd.Run()
	if err != nil && !strings.Contains(out.String(), "AlreadyExists") {
		return errors.New(strings.TrimSpace(out.String()))
	}
	return nil
}

func kubectlApplyFolder(folder string, provisioner provisioner.Provisioner) error {
	cmd, err := provisioner.KubectlCommand("apply", "-f", folder)
	if err != nil {
		return errors.Wrapf(err, "error while applying folder %s", folder)
	}
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf(strings.TrimSpace(out.String()))
	}
	return nil
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
	env, err := cfg.CurrentEnvironment(shl)
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

	if _, err := os.Stat(folderIn); err != nil {
		return cli.NewExitError(fmt.Sprintf("The input folder at %s could not be read. Does it exist? %s\nSee https://github.com/webappio/sanic-site for an example.", folderIn, err.Error()), 1)
	}
	err = os.MkdirAll(folderOut, 0750)
	if err != nil {
		return cli.NewExitError(fmt.Sprintf("The deployment output folder at %s could not be created: %s", folderOut, err.Error()), 1)
	}

	provisioner, err := getProvisioner()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	err = provisioner.EnsureCluster()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}
	err = runTemplater(folderIn, folderOut, cfg.Deploy.TemplaterImage, env.Namespace, cliContext.Args())
	if err != nil {
		return cli.NewExitError(fmt.Sprintf("could not compile templates: %s", err.Error()), 1)
	}
	if env.Namespace != "" {
		err = createNamespace(env.Namespace, provisioner)
		if err != nil {
			return cli.NewExitError(fmt.Sprintf(
				"namespace %s defined in sanic.yaml for this environment couldn't be created: %s",
				env.Namespace, err.Error(),
			), 1)
		}
	}
	err = kubectlApplyFolder(folderOut, provisioner)
	if err != nil {
		return cli.NewExitError(fmt.Sprintf("could not apply templates in %s: %s", folderOut, err.Error()), 1)
	}
	switch provisioner.(type){
	case *minikube.ProvisionerMinikube:
		fmt.Printf("Skip checking edge nodes for minikube\n")
	default:
		edgeNodes, err := provisioner.EdgeNodes()
		if err != nil {
			return cli.NewExitError(fmt.Sprintf("could not find edge routers: %s", err.Error()), 1)
		}
		if len(edgeNodes) == 0 {
			//this shouldn't happen: environment is misconfigured?
			return cli.NewExitError("there are no edge routers in this environment. Try reprovisioning your cluster", 1)
		}
		fmt.Printf("Configured HTTP services are available at http://%s\n", edgeNodes[rand.Intn(len(edgeNodes))])
	}
	return nil
}

var deployCommand = cli.Command{
	Name:   "deploy",
	Usage:  "deploy [service name...]",
	Action: deployCommandAction,
}
