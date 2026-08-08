package pnpminstall

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paketo-buildpacks/packit/v2/fs"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/scribe"
)

//go:generate faux --interface Summer --output fakes/summer.go
type Summer interface {
	Sum(paths ...string) (string, error)
}

//go:generate faux --interface Executable --output fakes/executable.go
type Executable interface {
	Execute(pexec.Execution) error
}

type PnpmInstallProcess struct {
	executable Executable
	summer     Summer
	logger     scribe.Emitter
}

func NewPnpmInstallProcess(executable Executable, summer Summer, logger scribe.Emitter) PnpmInstallProcess {
	return PnpmInstallProcess{
		executable: executable,
		summer:     summer,
		logger:     logger,
	}
}

func (ip PnpmInstallProcess) ShouldRun(workingDir string, metadata map[string]interface{}) (run bool, sha string, err error) {
	ip.logger.Subprocess("Process inputs:")

	_, err = os.Stat(filepath.Join(workingDir, "pnpm-lock.yaml"))
	if os.IsNotExist(err) {
		ip.logger.Action("pnpm-lock.yaml -> Not found")
		ip.logger.Break()
		return true, "", nil
	} else if err != nil {
		return true, "", fmt.Errorf("unable to read pnpm-lock.yaml file: %w", err)
	}

	ip.logger.Action("pnpm-lock.yaml -> Found")
	ip.logger.Break()

	buffer := bytes.NewBuffer(nil)

	err = ip.executable.Execute(pexec.Execution{
		Args:   []string{"config", "list"},
		Stdout: buffer,
		Stderr: buffer,
		Dir:    workingDir,
	})
	if err != nil {
		return true, "", fmt.Errorf("failed to execute pnpm config output:\n%s\nerror: %s", buffer.String(), err)
	}

	nodeEnv := os.Getenv("NODE_ENV")
	buffer.WriteString(nodeEnv)

	file, err := os.CreateTemp("", "config-file")
	if err != nil {
		return true, "", fmt.Errorf("failed to create temp file for %s: %w", file.Name(), err)
	}
	defer func() {
		if closeFileErr := file.Close(); closeFileErr != nil && err == nil {
			err = fmt.Errorf("failed to close temp file: %w", closeFileErr)
		}
	}()

	_, err = file.Write(buffer.Bytes())
	if err != nil {
		return true, "", fmt.Errorf("failed to write temp file for %s: %w", file.Name(), err)
	}

	sum, err := ip.summer.Sum(filepath.Join(workingDir, "pnpm-lock.yaml"), filepath.Join(workingDir, "package.json"), file.Name())
	if err != nil {
		return true, "", fmt.Errorf("unable to sum config files: %w", err)
	}

	prevSHA, ok := metadata["cache_sha"].(string)
	if (ok && sum != prevSHA) || !ok {
		return true, sum, nil
	}

	return false, "", nil
}

func (ip PnpmInstallProcess) SetupModules(workingDir, currentModulesLayerPath, nextModulesLayerPath string) (string, error) {
	nodeModulesPath := filepath.Join(workingDir, "node_modules")

	err := os.RemoveAll(nodeModulesPath)
	if err != nil {
		return "", fmt.Errorf("failed to remove existing node_modules: %w", err)
	}

	if currentModulesLayerPath != "" {
		err = fs.Copy(filepath.Join(currentModulesLayerPath, "node_modules"), nodeModulesPath)
		if err != nil {
			return "", fmt.Errorf("failed to copy cached node_modules to workspace: %w", err)
		}
	} else {
		err = os.MkdirAll(nodeModulesPath, os.ModePerm)
		if err != nil {
			return "", fmt.Errorf("failed to create workspace node_modules directory: %w", err)
		}
	}

	return nextModulesLayerPath, nil
}

func (ip PnpmInstallProcess) Execute(workingDir, modulesLayerPath string, launch bool) error {
	environment := os.Environ()
	environment = append(environment, fmt.Sprintf("PATH=%s%c%s", os.Getenv("PATH"), os.PathListSeparator, filepath.Join(workingDir, "node_modules", ".bin")))

	buffer := bytes.NewBuffer(nil)

	err := ip.executable.Execute(pexec.Execution{
		Args:   []string{"config", "get", "store-dir"},
		Stdout: buffer,
		Stderr: buffer,
		Env:    environment,
		Dir:    workingDir,
	})
	if err != nil {
		return fmt.Errorf("failed to execute pnpm config output:\n%s\nerror: %s", buffer.String(), err)
	}

	installArgs := []string{"install", "--frozen-lockfile"}

	var offlineStoreDir string
	for _, line := range strings.Split(buffer.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if filepath.IsAbs(trimmed) {
			offlineStoreDir = trimmed
		} else {
			offlineStoreDir = filepath.Join(workingDir, trimmed)
		}
		break
	}
	info, err := os.Stat(offlineStoreDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to confirm existence of offline store directory: %w", err)
	}

	if info != nil && info.IsDir() {
		installArgs = append(installArgs, "--offline")
	}

	if launch && os.Getenv("NODE_ENV") != "development" {
		installArgs = append(installArgs, "--prod")
	}

	ip.logger.Subprocess("Running 'pnpm %s'", strings.Join(installArgs, " "))

	err = ip.executable.Execute(pexec.Execution{
		Args:   installArgs,
		Env:    environment,
		Stdout: ip.logger.ActionWriter,
		Stderr: ip.logger.ActionWriter,
		Dir:    workingDir,
	})
	if err != nil {
		return fmt.Errorf("failed to execute pnpm install: %w", err)
	}

	destNodeModules := filepath.Join(modulesLayerPath, "node_modules")
	err = os.RemoveAll(destNodeModules)
	if err != nil {
		return fmt.Errorf("failed to clear destination layer node_modules: %w", err)
	}

	err = os.MkdirAll(modulesLayerPath, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create destination layer directory: %w", err)
	}

	err = fs.Move(filepath.Join(workingDir, "node_modules"), destNodeModules)
	if err != nil {
		return fmt.Errorf("failed to move node_modules to layer: %w", err)
	}

	err = os.Symlink(destNodeModules, filepath.Join(workingDir, "node_modules"))
	if err != nil {
		return fmt.Errorf("failed to symlink node_modules back to workspace: %w", err)
	}

	return nil
}
