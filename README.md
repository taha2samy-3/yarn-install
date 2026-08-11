# Paketo Buildpack for PNPM Install

The PNPM Install CNB generates and provides application dependencies for node
applications that use the [pnpm](https://pnpm.io) package manager.

## Integration

The PNPM Install CNB provides `node_modules` as a dependency. Downstream
buildpacks can require the `node_modules` dependency by generating a [Build
Plan TOML](https://github.com/buildpacks/spec/blob/master/buildpack.md#build-plan-toml)
file that looks like the following:

```toml
[[requires]]

  # The name of the PNPM Install dependency is "node_modules". This value is
  # considered part of the public API for the buildpack and will not change
  # without a plan for deprecation.
  name = "node_modules"

  # Note: The version field is unsupported as there is no version for a set of
  # node_modules.

  # The PNPM Install buildpack supports some non-required metadata options.
  [requires.metadata]

    # Setting the build flag to true will ensure that the node modules
    # are available for subsequent buildpacks during their build phase.
    # If you are writing a buildpack that needs a node module during
    # its build process, this flag should be set to true.
    build = true

    # Setting the launch flag to true will ensure that the packages
    # managed by PNPM are available for the running application. If you
    # are writing an application that needs node modules at runtime,
    # this flag should be set to true.
    launch = true

```

## Configuration

### Environment Variables

The following environment variables can be used to configure the behavior of this buildpack at build time:

| Variable | Description | Default |
|----------|-------------|---------|
| `BP_NODE_PROJECT_PATH` | Specifies a project subdirectory to be used as the root of the app. This is extremely useful if your app is part of a monorepo. | `<empty>` (workspace root) |
| `BP_PNPM_IN_LAUNCH` | Setting this to `false` prevents the `pnpm` executable requirement from being added to the launch environment. | `true` |
| `BP_PNPM_STRICT_BUILD_SCRIPTS` | Setting this to `true` opts out of automatically allowing dependency build scripts on pnpm 10+ (see [Dependency Build Scripts](#dependency-build-scripts-pnpm-10) below). | `false` |
| `BP_DISABLE_SBOM` | Setting this to `true` disables the generation of the Software Bill of Materials (SBOM), which can speed up the build process. | `false` |
| `NODE_ENV` | If set to anything other than `development` during the launch phase, the buildpack will append the `--prod` flag to `pnpm install` to exclude `devDependencies`. | `development` (during build) |

*(Note: Environment variables can be provided directly via `pack build --env` or through a [`project.toml` file](https://github.com/buildpacks/spec/blob/main/extensions/project-descriptor.md)).*

## Service Bindings

The PNPM Install buildpack supports providing configuration files securely via [Service Bindings](https://buildpacks.io/docs/features/bindings/). This is particularly useful for authenticating with private registries without baking credentials into your image.

### `npmrc` and `pnpmrc` bindings

You can provide a `.npmrc` or `.pnpmrc` file to the buildpack by creating a binding of type `npmrc` or `pnpmrc`. The buildpack will automatically symlink these files into the user's home directory during the build process. Since pnpm v10+ reads its configuration from `.npmrc` rather than `.pnpmrc`, a binding that only provides `.pnpmrc` is symlinked to both paths so registry/auth configuration still applies regardless of pnpm version.

**Example structure:**
```text
<binding-dir>/
├── type        # contains "npmrc" or "pnpmrc"
└── .npmrc      # your configuration/credentials
```

**Usage with `pack`:**
```shell
pack build my-app \
  --volume <absolute-path-to-binding-dir>:/platform/bindings/my-binding
```

## Features

### Offline Installation
This buildpack supports offline installations (e.g., for air-gapped environments). Before installing, the buildpack checks `pnpm config get store-dir`; if that directory already exists locally, `--offline` is automatically appended to the `pnpm install` command. This requires that all necessary packages are already present in the provided store directory.

### Layer Caching
Dependencies are cached in two independent layers, `build-modules` and `launch-modules`, each reused across builds when a checksum of the following inputs is unchanged: `pnpm-lock.yaml`, `package.json`, the output of `pnpm config list`, the current `NODE_ENV` value, and the resolved Node.js runtime version (`node --version`). The Node.js version is included specifically so that a change in the resolved Node.js version (for example, from a floating `engines.node` range picking up a new release) invalidates the cache — reusing a `node_modules` layer built against a different Node ABI can break any natively-compiled dependencies it contains.

### Dependency Build Scripts (pnpm 10+)
Starting with pnpm v10, dependency lifecycle scripts (`preinstall`, `install`, `postinstall`) are ignored by default as a supply-chain security measure — packages with native builds (e.g. `bcrypt`, `sharp`, `sqlite3`) will silently skip their build step unless explicitly approved, which normally requires an interactive prompt (`pnpm approve-builds`) that isn't available in a non-interactive buildpack build.

To avoid silently shipping unbuilt native dependencies, this buildpack automatically passes `--dangerously-allow-all-builds` to `pnpm install` when the resolved pnpm version is 10 or newer. This flag doesn't exist before pnpm v10 and is never added on older versions. Set `BP_PNPM_STRICT_BUILD_SCRIPTS=true` to opt out and keep pnpm's default script-blocking behavior instead.

### Workspaces (Monorepos)
This buildpack resolves the project root using the same `BP_NODE_PROJECT_PATH` convention as the existing npm and yarn buildpacks, so a `pnpm-lock.yaml`/`package.json` at a specified subdirectory of a larger repository is picked up correctly. It does not currently implement any `pnpm workspaces`-specific behavior (e.g. parsing `pnpm-workspace.yaml` or workspace-aware hoisting) beyond that project-path resolution.

## Usage

To package this buildpack for consumption:

```shell
$ ./scripts/package.sh --version <version-number>
```

This will create a `buildpackage.cnb` file under the `build` directory which you
can use to build your app as follows:
```shell
pack build <app-name> \
  --path <path-to-app> \
  --buildpack <path/to/node-engine.cnb> \
  --buildpack <path/to/pnpm.cnb> \
  --buildpack build/buildpackage.cnb
```

## Run Tests

To run all unit tests, run:
```shell
./scripts/unit.sh
```

To run all integration tests, run:
```shell
./scripts/integration.sh
```

## Stack support

For most apps, the PNPM Install Buildpack runs fine on the [Base builder](https://github.com/paketo-buildpacks/stacks#metadata-for-paketo-buildrun-stack-images). 
But when the app requires compilation of native extensions using `node-gyp`, the buildpack requires that you use the [Full builder](https://github.com/paketo-buildpacks/stacks#metadata-for-paketo-buildrun-stack-images). This is because `node-gyp` requires `python` that's absent on the Base builder, and the module may require other shared objects.