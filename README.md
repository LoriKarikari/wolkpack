# terril

terril builds a container image from a Node, Python, Ruby, PHP, .NET, Rust or Go app.

```sh
terril build ./my-app
docker run -p 8080:8080 my-app
```

## How it works

terril reads the app's manifest and lockfile to work out how to install, build and start it. It turns that into a Dockerfile and builds it with Docker.

The app builds in one image and runs in a separate, smaller one that contains only what it needs at runtime. That image runs as a non-root user and listens on `$PORT`, which defaults to 8080.

For Node, Python, PHP, .NET, Rust, Go and static sites, the runtime image is distroless, so it has no shell or package manager. Those apps get a Debian image instead when their start command needs a shell or `terril.toml` adds apt packages. Ruby apps run on the official `ruby` slim image.

## Install

terril runs on Linux and macOS, on amd64 and arm64. It needs Docker 23 or later.

Download the archive for your system from the [releases page](https://github.com/LoriKarikari/terril/releases), unpack it, and put `terril` on your `PATH`. `checksums.txt` lists each archive's SHA-256.

With Go 1.27 or later, you can install it from source instead:

```sh
go install github.com/LoriKarikari/terril/cmd/terril@latest
```

## Use

Print the Dockerfile terril would build:

```sh
terril plan ./my-app
```

Build the image. It gets the folder's name as its tag, here `my-app`:

```sh
terril build ./my-app
```

Build the image with your own tag:

```sh
terril build -t api:dev ./my-app
```

## Supported apps

| Language | Package managers | Frameworks |
|---|---|---|
| Node | npm, pnpm, Yarn, Bun | Next.js, Nuxt, Astro, React Router, or any app with a `start` script |
| Python | pip, uv, Poetry | Django, FastAPI, Flask |
| Ruby | Bundler | Rails, or any Rack app with puma |
| PHP | Composer | Laravel, or any app with `public/index.php` or `index.php` |
| .NET | NuGet | ASP.NET Core and console apps |
| Rust | Cargo | any binary crate, including workspaces |
| Go | Go modules | any, including apps that use cgo |
| Static sites | none, or any Node build | plain HTML, Vite apps without a server |

A `web:` line in a `Procfile` replaces the start command.

A folder with only an `index.html`, or a Vite app with no `start` script, is served as a static site by Caddy. Vite apps get single-page-app routing, so `/dashboard` serves `index.html`.

If a Python, Ruby, PHP or Go app has a `package.json` with a `build` script, Node runs that script first. The final image doesn't include Node. For Django apps that set `STATIC_ROOT`, terril then runs `collectstatic`. Rails apps get `assets:precompile`. Rails needs `SECRET_KEY_BASE` or `RAILS_MASTER_KEY` when the container starts, and terril never puts either in the image.

PHP apps run on [FrankenPHP](https://frankenphp.dev)'s static build, which has PHP 8.5 and 80 extensions, such as intl, gd, pdo_pgsql and redis. The same binary runs Composer, so an app that needs another extension fails at `composer install` and names it. Laravel apps need `APP_KEY` when the container starts, and log to stderr.

For .NET, terril publishes the project at the app root, or the only web project in the folder, such as an API next to its test projects. .NET apps run on Microsoft's chiseled images, with ICU for culture formatting unless the project sets `InvariantGlobalization`. A `global.json` that pins an SDK version picks that exact SDK.

For Rust, terril builds the only binary in the crate or workspace, or the one `default-run` names. Dependencies compile in their own cached step with [cargo-chef](https://github.com/LukeMathWalker/cargo-chef), so editing your code doesn't recompile them. A version in `rust-toolchain.toml` picks that Rust version.

terril builds only the Node, Python, Ruby and .NET versions that still get security fixes, by the dates on [endoflife.date](https://endoflife.date). From six months before a version's end of life, the build prints a warning with the date. Two months after it, terril refuses to build that version and names the ones it builds. Go apps build with the newest Go, because the `go` line in `go.mod` is a minimum.

## Build-time variables

Some builds need values that aren't in the repo, such as `VITE_API_URL` or a token for a private package registry. Pass each one with `--env`:

```sh
terril build --env VITE_API_URL=https://api.example.com ./my-app
```

Without `=VALUE`, terril takes the value from your environment:

```sh
NPM_TOKEN=... terril build --env NPM_TOKEN ./my-app
```

The build steps can read these variables, but the running app can't, and the values aren't stored in the image. When a value changes, terril rebuilds the steps that read it. Set runtime variables when you start the container, for example with `docker run -e`.

## Monorepos

Point terril at one app in a JavaScript or uv workspace:

```sh
terril build apps/web
```

terril finds the workspace root. It builds the workspace packages the app depends on, and then the app. For JavaScript it uses turbo or pnpm if the repo has them. The image contains only the app's production dependencies.

## Config

To change the defaults, add a `terril.toml` next to the app:

```toml
provider = "python"            # pick a language when the repo has two
start = "python serve.py"      # start command, run through sh
packages = ["libpq5"]          # apt packages for the build and the image
build-packages = ["libpq-dev"] # apt packages for the build only
```

terril rejects unknown keys, so a typo fails the build.

## Acknowledgements

terril is inspired by [Nixpacks](https://github.com/railwayapp/nixpacks), [Railpack](https://github.com/railwayapp/railpack) and [Cloud Native Buildpacks](https://buildpacks.io). Thank you to their maintainers and contributors.

## License

Apache-2.0. See `LICENSE`.
