# terril

terril is an open-source CLI that builds a container image from a Node, Python or Go app, or a static site. It reads the app's manifests, writes a Dockerfile, and runs `docker build`. `terril plan` prints that Dockerfile.

## Code

- `pack/facts.go` reads and parses every manifest, lockfile and `terril.toml`. Nothing else reads app files.
- `pack/pack.go` picks the providers and their roles. The primary provider owns the runtime image. Node can also build assets for a Python or Go app.
- One file per language in `pack/` (`node.go`, `python.go`, `ruby.go`, `php.go`, `dotnet.go`, `rust.go`, `golang.go`, `static.go`) turns facts into build stages and a runtime.
- `pack/version.go` lists the Node, Python, Ruby and .NET versions terril builds, each with its end-of-life date. A new version needs its date.
- `pack/plan.go` renders a plan to a Dockerfile.
- `cmd/terril` is the CLI.

## Commands

- `just build` builds `bin/terril`.
- `just test` runs the end-to-end tests in `e2e/e2e_test.go`. `just test python-flask` runs one app.
- `just cache` checks that a source edit doesn't rerun the dependency install.
- `just lint` runs golangci-lint (with gofmt and goimports) and govulncheck, the same checks CI runs. `just fmt` formats the code.

## Rules

- The CLI flags, the `terril.toml` keys and their errors are a contract. Changing them needs an issue first.
- Parse manifests with real parsers in `pack/facts.go`. Providers read typed facts, never raw file text.
- When two answers are equally likely, fail with an error that names both. Never pick one by list order.
- Images run as UID 1000. A change that makes any image run as root is wrong.
- Images contain only what the app needs at runtime. No dev dependencies, no host `node_modules` or `__pycache__`, no build tools.
- Pin every tool version terril installs, such as uv, Poetry and corepack. Put each version in its own `const` with a `// renovate: datasource=... depName=...` comment above it, so Renovate keeps it current.
- Every change gets an end-to-end test app in `e2e/testdata/` that fails without the change. Don't write unit tests.
- Title changes to CI, tests or tooling `ci:`, `test:` or `chore:`, never `fix(ci):`. `feat`, `fix` and `perf` titles go into the release notes.
- Write a comment only when the code can't show why it's written that way. Never narrate what the code does.
