package pnpminstall

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	executable     Executable
	nodeExecutable Executable // CHANGE: separate executable bound to "node", used only to read the resolved runtime version for cache-checksum purposes below. `executable` above is bound to "pnpm" and cannot be reused for this.
	summer         Summer
	logger         scribe.Emitter
}

// CHANGE: NewPnpmInstallProcess now takes a second Executable, bound to the
// "node" binary. See the call site in run/main.go for how it's constructed.
func NewPnpmInstallProcess(executable Executable, nodeExecutable Executable, summer Summer, logger scribe.Emitter) PnpmInstallProcess {
	return PnpmInstallProcess{
		executable:     executable,
		nodeExecutable: nodeExecutable,
		summer:         summer,
		logger:         logger,
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

	// CHANGE: include the resolved Node.js runtime version in the cache checksum.
	//
	// Without this, a cached node_modules layer can be reused across builds even
	// when the node-engine buildpack resolves a *different* Node.js version between
	// them (e.g. a floating "engines.node": "18.x" range picking up a new patch/minor
	// release). If the app has any natively-compiled dependencies (node-gyp/N-API
	// addons), the cached binaries were built against the old Node ABI, and reusing
	// them against a new Node runtime can fail or crash at launch instead of
	// triggering a rebuild.
	//
	// node-engine does not export its resolved version as an env var (it's only
	// used for its own log output), so we ask the node binary itself via a
	// dedicated "node" Executable (nodeExecutable) — `ip.executable` above is bound
	// to "pnpm" specifically and can't be reused for this. `node` is already on
	// $PATH via the NODE_HOME layer that node-engine shares with every buildpack
	// that runs after it in the same order group, so no extra resolution is needed.
	nodeVersionBuffer := bytes.NewBuffer(nil)
	err = ip.nodeExecutable.Execute(pexec.Execution{
		Args:   []string{"--version"},
		Stdout: nodeVersionBuffer,
		Stderr: nodeVersionBuffer,
		Dir:    workingDir,
	})
	if err != nil {
		return true, "", fmt.Errorf("failed to determine node version:\n%s\nerror: %s", nodeVersionBuffer.String(), err)
	}
	buffer.WriteString(nodeVersionBuffer.String())

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
	} else if !launch {
		environment = append(environment, "NODE_ENV=development")
	}

	// Both checks below need to know the resolved pnpm major version, so it's
	// fetched once and reused, rather than shelling out to `pnpm --version`
	// twice. It's skipped entirely when neither check is active, to avoid an
	// unnecessary process execution on the common path.
	allowBuildScripts := os.Getenv("BP_PNPM_STRICT_BUILD_SCRIPTS") != "true"
	// PNPM_VERSION_MISMATCH is set by the pnpm buildpack (via SharedEnv) when
	// the pnpm version it delivered doesn't exactly match what was requested
	// (e.g. package.json's "packageManager" field asked for a patch release
	// this buildpack's dependency metadata doesn't have yet, and it fell back
	// to the closest available version in the same major instead). Since
	// pnpm v9+, pnpm itself refuses to run at all when its own version
	// doesn't exactly match "packageManager" — so this needs to be relaxed
	// specifically for that case, not for every build.
	handleVersionMismatch := os.Getenv("PNPM_VERSION_MISMATCH") == "true"

	var pnpmMajor int
	if allowBuildScripts || handleVersionMismatch {
		pnpmVersionBuffer := bytes.NewBuffer(nil)
		err = ip.executable.Execute(pexec.Execution{
			Args:   []string{"--version"},
			Stdout: pnpmVersionBuffer,
			Stderr: pnpmVersionBuffer,
			Dir:    workingDir,
		})
		if err != nil {
			return fmt.Errorf("failed to determine pnpm version:\n%s\nerror: %s", pnpmVersionBuffer.String(), err)
		}

		pnpmMajor, err = parsePnpmMajorVersion(pnpmVersionBuffer.String())
		if err != nil {
			return fmt.Errorf("failed to parse pnpm version %q: %w", pnpmVersionBuffer.String(), err)
		}

		// lightweight sanity check, not a hard requirement — warns instead of
		// blocking, since pnpm's own error on a real mismatch is clear enough
		// on its own; this just gets ahead of it with better context. Only
		// checked here, piggybacking on the pnpm version already being
		// fetched above for one of the two checks below, rather than
		// unconditionally, to avoid an extra process execution on every
		// single install just for this.
		if pnpmMajor >= 11 {
			nodeVersionBuffer := bytes.NewBuffer(nil)
			nodeErr := ip.nodeExecutable.Execute(pexec.Execution{
				Args:   []string{"--version"},
				Stdout: nodeVersionBuffer,
				Stderr: nodeVersionBuffer,
				Dir:    workingDir,
			})
			// Best-effort only: if the node version can't be determined or
			// parsed for some reason, this check is silently skipped rather
			// than failing the build over an informational warning.
			if nodeErr == nil {
				if nodeMajor, parseErr := parsePnpmMajorVersion(nodeVersionBuffer.String()); parseErr == nil && nodeMajor < 22 {
					ip.logger.Subprocess("Warning: pnpm %d requires Node.js 22+, but the resolved Node.js version is %d.x — install may fail.", pnpmMajor, nodeMajor)
				}
			}
		}
	}

	// CHANGE: pnpm v10+ blocks dependency lifecycle scripts (preinstall/install/
	// postinstall) by default as a supply-chain security measure, and the only
	// way to approve them non-interactively is --dangerously-allow-all-builds.
	// Without this, packages with native builds (bcrypt, sharp, sqlite3, etc.)
	// silently skip their build step — the install still succeeds, but the app
	// crashes at launch when it tries to load the unbuilt native module.
	//
	// The flag itself doesn't exist before pnpm v10 and pnpm hard-fails with
	// "Unknown option" on any unrecognized CLI flag, so this must only be added
	// when the resolved pnpm version actually supports it — never unconditionally.
	//
	// Default is to add it automatically (opt-out via BP_PNPM_STRICT_BUILD_SCRIPTS),
	// following the same pattern Paketo's own ca-certificates buildpack uses
	// (behavior on by default, explicit env var to opt out) — a CNB build runs
	// in an isolated, ephemeral environment, which is a different trust context
	// than the long-lived developer machine pnpm's default is designed to protect.
	if allowBuildScripts && pnpmMajor >= 10 {
		installArgs = append(installArgs, "--dangerously-allow-all-builds")
	}

	// CHANGE: relax pnpm's own packageManager-version enforcement, but only
	// when the pnpm buildpack has told us (via PNPM_VERSION_MISMATCH) that it
	// had to substitute a different version than what was requested. In that
	// specific case, running `pnpm install` normally would fail outright with
	// ERR_PNPM_BAD_PM_VERSION, since pnpm refuses to run when its own version
	// doesn't exactly match package.json's "packageManager" field. Left alone
	// in the (much more common) case where the version matches exactly, since
	// that check is a legitimate reproducibility guarantee for users who rely
	// on it, not something this buildpack should routinely bypass.
	//
	// The setting to relax this has changed name and location across pnpm
	// versions — v9 and v10 read `package-manager-strict` (as a CLI --config
	// flag here, since it's the one part of pnpm's config surface that hasn't
	// moved between .npmrc and pnpm-workspace.yaml), while v11 replaced it
	// entirely with `pmOnFail` and no longer honors the old setting.
	if handleVersionMismatch {
		switch {
		case pnpmMajor >= 11:
			installArgs = append(installArgs, "--config.pmOnFail=warn")
		case pnpmMajor >= 9:
			installArgs = append(installArgs, "--config.package-manager-strict=false")
		}
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

// parsePnpmMajorVersion extracts the major version number from `pnpm --version`
// output (e.g. "10.15.9\n" -> 10, "9.15.9" -> 9). Used to gate CLI flags that
// only exist on newer pnpm releases, since pnpm hard-fails on unrecognized flags.
func parsePnpmMajorVersion(versionOutput string) (int, error) {
	trimmed := strings.TrimSpace(versionOutput)
	trimmed = strings.TrimPrefix(trimmed, "v")

	parts := strings.SplitN(trimmed, ".", 2)
	if len(parts) == 0 || parts[0] == "" {
		return 0, fmt.Errorf("unrecognized version format")
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("unrecognized version format: %w", err)
	}

	return major, nil
}
