# Contributing to terril

Thanks for wanting to help. This page says which changes terril accepts and how to prove them, so nobody spends a weekend on a pull request that gets closed.

## What's welcome, and what isn't

### Send a pull request

- **Bug fixes.** terril builds an image that's wrong, too big, or doesn't start, and you have a fix.
- **Package managers and frameworks.** Support for one that a language already almost handles, such as a new lockfile or a framework's start command, with a test app that proves it.
- **Docs fixes.** Something is unclear, outdated or wrong.
- **Small improvements.** A clearer error, a smaller image, a faster build, or a cleanup that touches a few files.

### Open an issue first

- **New languages.** Each one means a new provider and long-term upkeep. Describe the apps you want to build first.
- **Changes to the contract.** Anything that changes CLI flags, `terril.toml` keys, or how terril detects an app affects every user.
- **Anything large.** If it touches many files, check that it fits before you build it.

### Usually declined

- **A custom plan format or BuildKit frontend.** The plan is a Dockerfile on purpose, so anyone can read it and build it without terril.
- **Guessing.** Detection that picks a language or start command when two are equally likely. terril fails and asks instead.

## Pull requests

1. Read `AGENTS.md`. It lists the rules every change follows, such as running images as a non-root user and parsing manifests only in `pack/facts.go`.
2. Fork the repo and branch from `main`.
3. Keep the change focused on one thing. Unrelated cleanups go in their own pull request.
4. Prove it end to end with a test app. See the testing section below.
5. Update `README.md` if behavior changes.
6. Run `just lint` and `just test`. Install [just](https://github.com/casey/just) first if you don't have it. Both must pass.
7. Write commit messages and the pull request title as [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
8. Describe what the change does, what it doesn't do, and how you checked it.

`main` only accepts squash merges, and the `e2e` check must pass. The pull request title becomes the commit on `main`.

### Commit messages

Use `<type>(<scope>): <summary>`, in lowercase and the imperative mood, with no period at the end.

- **Types.** `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, and `chore`.
- **CI changes.** Use `ci`, even for a fix. `feat`, `fix` and `perf` are for changes users of terril notice, and they go into the release notes.
- **Scopes.** Optional. Use the language or area the change touches, such as `node`, `python`, `go`, `static`, `config`, or `plan`.
- **Breaking changes.** Add `!` after the type or scope, and explain the break in a `BREAKING CHANGE:` footer. Anything that changes CLI flags or `terril.toml` in an incompatible way counts.

For example:

```
fix(node): prune Yarn workspace builds
feat(python): start Litestar apps with uvicorn
docs: explain how terril picks the start command
feat(config)!: rename packages to apt-packages
```

## Testing

terril has one kind of test. `e2e/e2e_test.go` builds every app in `e2e/testdata/` with the terril CLI, starts it, and checks its HTTP response. You need Docker and [dive](https://github.com/wagoodman/dive).

Run every app, or only some:

```sh
just test
just test python-flask node-pnpm
```

The run prints a table and writes it to `e2e/results.tsv`, with each image's size on disk, its compressed pull size, dive's efficiency and wasted bytes, and its user. A test fails if the image runs as root, or if dive's efficiency score is below 95%, which means files are duplicated or deleted across layers. Build logs go to `e2e/logs/`.

Apps build four at a time. Each test removes its images when it ends, even when it fails, and the run trims Docker's build cache to 10 GB at the end. Pass `-keep` to `go test` to keep the images.

### Add a test app

Make a folder in `e2e/testdata/` with a small app that listens on `$PORT`. Add an `expect` file with text the response must contain.

A test app can also have these files:

- `expect-error` holds a message. The build must fail with it, and the app isn't started.
- `expect-warning` holds a warning the build must print.
- `today` holds a date like `2026-11-01`. terril builds the app as if it were that day, through the `TERRIL_TODAY` variable. Use it to test end-of-life warnings and refusals, which change with the calendar.
- `path` holds the URL path to request. The default is `/`.
- `app` names a subfolder to build, for monorepos.
- `absent` lists package names that must not be in the image.
- `run-env` holds `KEY=VALUE` lines that the test passes to `docker run -e`, for values a platform sets at runtime, such as secrets.
- `build-env` holds `KEY=VALUE` lines that the test passes with `--env`. It builds once with other values first, so a cached build fails the test. A line that starts with `secret ` must not appear anywhere in the image. Add `build-env` to the app's `.dockerignore`. No `--env` value may appear in the image's history or config.

Before each build, the test adds a `node_modules` and a `__pycache__` folder to the app. The test fails if either ends up in the image.

Commit lockfiles, and leave out `node_modules`, `.venv` and build output.

`just cache` builds each app, adds a source file, and rebuilds. It fails if the rebuild reruns the dependency install, and writes the rebuild time to `e2e/cache.tsv`. It builds one app at a time, because parallel builds that share steps can miss Docker's cache. CI runs it on every pull request that changes code.

## Releases

[release-please](https://github.com/googleapis/release-please) keeps a release pull request open on `main`. Every merged `feat:`, `fix:` or `perf:` pull request adds to its `CHANGELOG.md` and bumps the version it proposes. Before 1.0, `feat:` bumps the minor version and `fix:` the patch version. CI on a pull request that Actions opens waits for approval, so the release workflow approves it. Merging the release pull request tags the release, and GoReleaser then builds `terril` for Linux and macOS on amd64 and arm64 and attaches the archives and `checksums.txt` to it.

## Using AI tools

AI-assisted pull requests and issues are fine. They meet the same bar as any other change:

- **Show it's real.** Include the actual `terril` output, build log, or container error that proves the problem exists.
- **Say what's in scope.** Name what the change does and what it leaves alone.
- **Include how to check it.** The exact commands you ran, not a description of them.
- **Review it yourself first.** You're responsible for every line you submit.

Agents should follow `AI_CONTRIBUTING.md`, which walks through each step.

## Bug reports

Open an issue with the bug report form. It asks for the terril commit, the command you ran, the output of `terril plan`, and what you expected.

## Security

Don't open a public issue for a security problem. Report it privately through GitHub's private vulnerability reporting on this repository, with the terril commit and steps to reproduce.

## License

terril uses the Apache-2.0 license. By submitting a pull request, you agree that your contribution is licensed the same way.
