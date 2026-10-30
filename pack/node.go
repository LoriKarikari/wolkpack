package pack

import (
	"fmt"
	"path"
	"strings"
)

var nodeProvider = provider{
	name: "node",
	detect: func(f *Facts) Role {
		if f.Node == nil {
			return 0
		}
		// A package.json alone (prettier, husky) is not a frontend build.
		if f.Node.Package.Scripts["build"] != "" {
			return Primary | Assets
		}
		return Primary
	},
	plan:     planNode,
	versions: &nodeVersions,
	version: func(f *Facts) (string, error) {
		if f.Node.Manager == "bun" {
			return "", nil
		}
		return nodeVersion(f)
	},
	assetStage: func(f *Facts, name string) (Stage, error) {
		return nodeBuild(f, name, nil, false)
	},
}

type nodeManager struct {
	image               func(f *Facts) (string, error)
	setup               func(n *Node) []Step
	install, frozen     string
	prune, cache        string
	run, start, runFile string
	exec                string
	// Reinstalls only one workspace member's production dependency tree.
	workspacePrune func(install, pkg string) string
}

// renovate: datasource=npm depName=corepack
const corepackVersion = "0.36.0"

var corepackYarn = func(*Node) []Step {
	return []Step{
		Env{"COREPACK_ENABLE_DOWNLOAD_PROMPT", "0"},
		Env{"COREPACK_ENABLE_AUTO_PIN", "0"},
		Run{Cmd: "npm install -g --force corepack@" + corepackVersion + " && corepack enable yarn", Caches: []string{"/root/.npm"}},
	}
}

var nodeManagers = map[string]nodeManager{
	"npm": {
		image: nodeImage, install: "npm install", frozen: "npm ci",
		prune: "npm prune --omit=dev", cache: "/root/.npm",
		run: "npm run", start: "npm start", runFile: "node", exec: "npx",
		workspacePrune: func(install, pkg string) string {
			return install + " --omit=dev --include-workspace-root --workspace=" + shellQuote([]string{pkg})
		},
	},
	"pnpm": {
		image: nodeImage,
		setup: func(n *Node) []Step {
			v := n.ManagerVersion
			if v == "" {
				v = "latest"
			}
			return []Step{Run{Cmd: "npm install -g pnpm@" + v, Caches: []string{"/root/.npm"}}}
		},
		install: "pnpm install", frozen: "pnpm install --frozen-lockfile",
		prune: "pnpm prune --prod", cache: "/root/.local/share/pnpm/store",
		run: "pnpm run", start: "npm start", runFile: "node", exec: "pnpm exec",
		workspacePrune: func(install, pkg string) string {
			return install + " --prod --filter " + shellQuote([]string{pkg + "..."})
		},
	},
	"yarn": {
		image: nodeImage, setup: corepackYarn,
		install: "yarn install", frozen: "yarn install --frozen-lockfile",
		prune: "yarn install --production --frozen-lockfile --ignore-scripts --prefer-offline", cache: "/usr/local/share/.cache/yarn",
		run: "yarn run", start: "npm start", runFile: "node", exec: "yarn",
		// Yarn 1 can't install one member alone, so other members' production dependencies stay.
		workspacePrune: func(install, _ string) string { return install + " --production" },
	},
	"yarn-berry": {
		image: nodeImage,
		setup: func(n *Node) []Step {
			// Plug'n'Play loads packages from the cache at runtime, so it has to live in the image.
			return append(corepackYarn(n), Env{"YARN_ENABLE_GLOBAL_CACHE", "false"})
		},
		install: "yarn install", frozen: "yarn install --immutable",
		prune: "yarn workspaces focus --all --production",
		run:   "yarn run", start: "npm start", runFile: "node", exec: "yarn",
		// focus leaves unused zips in the cache, so it refills an empty one.
		workspacePrune: func(_, pkg string) string {
			return "rm -rf .yarn/cache && yarn workspaces focus --production " + shellQuote([]string{pkg})
		},
	},
	"bun": {
		image:   func(f *Facts) (string, error) { return "oven/bun:" + bunVersion(f.Node) + "-slim", nil },
		install: "bun install", frozen: "bun install --frozen-lockfile",
		prune: "rm -rf node_modules && bun install --production --frozen-lockfile", cache: "/root/.bun/install/cache",
		run: "bun run", start: "bun run start", runFile: "bun", exec: "bunx",
		workspacePrune: func(install, pkg string) string {
			return install + " --production --filter " + shellQuote([]string{pkg})
		},
	},
}

func (m nodeManager) caches() []string {
	if m.cache == "" {
		return nil
	}
	return []string{m.cache}
}

func bunVersion(n *Node) string {
	if n.ManagerVersion != "" {
		return n.ManagerVersion
	}
	return "1"
}

// Bun's distroless image has no shell, so the start script must be one bun or node call.
func distrolessBun(f *Facts) (string, []Env, Cmd, bool) {
	if f.needsDebianRuntime() {
		return "", nil, nil, false
	}
	var env []Env
	start, err := nodeStart(f)
	if err != nil {
		return "", nil, nil, false
	}
	if script := f.Node.Package.Scripts["start"]; script != "" {
		var args []string
		var ok bool
		if env, args, ok = directCommand(script); !ok || (args[0] != "bun" && args[0] != "node") || len(args) < 2 {
			return "", nil, nil, false
		}
		start = append(Cmd{"bun"}, args[1:]...)
	}
	return "oven/bun:" + bunVersion(f.Node) + "-distroless", env, start, true
}

// Files in node_modules that Node never loads to run an app. License files stay, because
// many licenses require shipping them. Type declarations stay when the app type-checks at
// runtime.
func nodeModulesExcludes(f *Facts) []string {
	excludes := []string{
		"**/node_modules/**/*.map",
		"**/node_modules/**/README.md",
		"**/node_modules/**/readme.md",
		"**/node_modules/**/CHANGELOG.md",
		"**/node_modules/**/HISTORY.md",
	}
	deps := f.Node.Package.Dependencies
	if deps["typescript"] == "" && deps["ts-node"] == "" && deps["tsx"] == "" {
		excludes = append(excludes, "**/node_modules/**/*.d.ts", "**/node_modules/**/*.d.mts", "**/node_modules/**/*.d.cts")
	}
	return excludes
}

func nodeVersion(f *Facts) (string, error) {
	return nodeVersions.pick(f.Node.Version, f.Today)
}

// Trixie matches distroless debian13, so native modules built here load at runtime.
func nodeImage(f *Facts) (string, error) {
	v, err := nodeVersion(f)
	return "node:" + v + "-trixie-slim", err
}

func nodeBuild(f *Facts, name string, assets []string, prune bool) (Stage, error) {
	n := f.Node
	m, ok := nodeManagers[n.Manager]
	if !ok {
		return Stage{}, fmt.Errorf("%s isn't supported yet; use npm, pnpm, yarn or bun", n.Manager)
	}
	image, err := m.image(f)
	if err != nil {
		return Stage{}, err
	}

	var steps []Step
	if m.setup != nil {
		steps = append(steps, m.setup(n)...)
	}
	if fw := nodeFrameworkFor(f); fw != nil {
		for _, e := range fw.buildEnv {
			steps = append(steps, e)
		}
	}
	install := m.install
	if n.Lockfile != "" {
		install = m.frozen
	}
	if f.Workspace != nil {
		build, err := workspaceBuild(f, m)
		if err != nil {
			return Stage{}, err
		}
		// The install reads every member's package.json, but not their source.
		manifests := []string{"package.json"}
		for _, name := range []string{n.Lockfile, "pnpm-workspace.yaml", ".yarnrc.yml", ".yarn/releases", ".yarn/plugins", ".yarn/patches", "patches"} {
			if name != "" && f.rootHas(name) {
				manifests = append(manifests, name)
			}
		}
		for _, dir := range f.Workspace.Dirs {
			manifests = append(manifests, dir+"/package.json")
		}
		steps = append(steps,
			Copy{Src: manifests, Dst: "./", Parents: true},
			Run{Cmd: install, Caches: m.caches()},
			copySource(),
		)
		if build != "" {
			steps = append(steps, Run{Cmd: build})
		}
		if prune && m.workspacePrune != nil {
			steps = append(steps, Run{
				Cmd:    "find . -name node_modules -type d -prune -exec rm -rf {} + && " + m.workspacePrune(install, n.Package.Name),
				Caches: m.caches(),
			})
		}
		return Stage{Name: name, From: image, Steps: steps}, nil
	}
	manifests := []string{"package.json"}
	if n.Lockfile != "" {
		manifests = append(manifests, n.Lockfile)
	}
	if f.Has(".yarnrc.yml") {
		manifests = append(manifests, ".yarnrc.yml")
	}
	steps = append(steps,
		Copy{Src: manifests, Dst: "./"},
		Run{Cmd: install, Caches: m.caches()},
		copySource(),
	)
	steps = append(steps, copyAssets(assets)...)
	if n.Package.Scripts["build"] != "" {
		steps = append(steps, Run{Cmd: m.run + " build"})
	}
	if prune {
		steps = append(steps, Run{Cmd: m.prune, Caches: m.caches()})
	}
	return Stage{Name: name, From: image, Steps: steps}, nil
}

func workspaceBuild(f *Facts, m nodeManager) (string, error) {
	name := f.Node.Package.Name
	if name == "" {
		return "", fmt.Errorf("%s/package.json has no name, which workspace tools need to select it", f.Workspace.App)
	}
	switch {
	case f.rootHas("turbo.json"):
		return m.exec + " turbo run build --filter=" + shellQuote([]string{name}), nil
	case f.Node.Manager == "pnpm":
		return "pnpm --filter " + shellQuote([]string{name + "..."}) + " run build", nil
	}
	var builds []string
	for _, dir := range buildOrder(f.Workspace.Members, name) {
		builds = append(builds, "(cd "+shellQuote([]string{dir})+" && "+m.run+" build)")
	}
	return strings.Join(builds, " && "), nil
}

// buildOrder lists the dirs of name and its workspace dependencies that have a build
// script, dependencies first.
func buildOrder(members map[string]Member, name string) []string {
	var order []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		m, ok := members[n]
		if !ok || seen[n] {
			return
		}
		seen[n] = true
		for _, d := range m.Deps {
			visit(d)
		}
		if m.Build {
			order = append(order, m.Dir)
		}
	}
	visit(name)
	return order
}

func nodeStart(f *Facts) (Cmd, error) {
	pkg := f.Node.Package
	m := nodeManagers[f.Node.Manager]
	if pkg.Scripts["start"] != "" {
		return Cmd(strings.Fields(m.start)), nil
	}
	for _, file := range []string{pkg.Main, "server.js", "index.js", "server.ts", "index.ts"} {
		if file != "" && f.Has(file) {
			return Cmd{m.runFile, file}, nil
		}
	}
	return nil, fmt.Errorf("no start command: add a \"start\" script to package.json, or a server.js")
}

// Distroless has no shell or npm, so it only runs a start command that is a plain node call.
// Distroless has no shell or npm, so the start script must be one node call, or one call to a
// package bin that package-lock.json maps to a file. check verifies that file is a Node script.
func distrolessNode(f *Facts) (image string, env []Env, cmd Cmd, check *Run, ok bool) {
	v, _ := nodeVersion(f)
	if f.needsDebianRuntime() || nodeManagers[f.Node.Manager].runFile != "node" || strings.Contains(v, ".") {
		return "", nil, nil, nil, false
	}
	start, err := nodeStart(f)
	if err != nil {
		return "", nil, nil, nil, false
	}
	if script := f.Node.Package.Scripts["start"]; script != "" {
		var args []string
		if env, args, ok = directCommand(script); !ok {
			return "", nil, nil, nil, false
		}
		if bin, found := f.Node.Bins[args[0]]; found && args[0] != "node" {
			args = append([]string{"node", bin}, args[1:]...)
			check = &Run{Cmd: "case \"$(head -c 64 " + shellQuote([]string{bin}) + ")\" in '#!'*node*) ;; *) " +
				"echo " + shellQuote([]string{"terril: " + bin + " isn't a Node script, so it can't run on distroless; set start in terril.toml"}) + " >&2; exit 1;; esac"}
		}
		if args[0] != "node" || len(args) < 2 {
			return "", nil, nil, nil, false
		}
		start = Cmd(args)
	}
	return "gcr.io/distroless/nodejs" + v + "-debian13", env, append(Cmd{"/nodejs/bin/node"}, start[1:]...), check, true
}

func planNode(f *Facts, assets []string) ([]Stage, Runtime, error) {
	if fw := nodeFrameworkFor(f); fw != nil {
		build, err := nodeBuild(f, "build", assets, false)
		if err != nil {
			return nil, Runtime{}, err
		}
		rt := Runtime{
			From:   build.From,
			Copies: fw.copies(f),
			Env:    append([]Env{{"NODE_ENV", "production"}, {"HOST", "0.0.0.0"}}, fw.env...),
			Cmd:    append(Cmd{"node"}, fw.cmd...),
		}
		if v, _ := nodeVersion(f); !f.needsDebianRuntime() && !strings.Contains(v, ".") {
			rt.From = "gcr.io/distroless/nodejs" + v + "-debian13"
			rt.Cmd[0] = "/nodejs/bin/node"
		}
		return []Stage{build}, rt, nil
	}
	build, err := nodeBuild(f, "build", assets, true)
	if err != nil {
		return nil, Runtime{}, err
	}
	start, err := nodeStart(f)
	if err != nil && f.Start == "" {
		out := viteOutDir(f)
		if out == "" {
			return nil, Runtime{}, err
		}
		app := ""
		if f.Workspace != nil {
			app = f.Workspace.App
		}
		site := Copy{From: "build", Src: []string{path.Join("/app", app, out) + "/"}, Dst: "/srv/"}
		rt, err := staticRuntime(f, site, true)
		return []Stage{build}, rt, err
	}
	// Astro, Nitro and others listen on HOST, often localhost by default.
	env := []Env{{"NODE_ENV", "production"}, {"HOST", "0.0.0.0"}}
	if f.Node.PnP {
		env = append(env, Env{"NODE_OPTIONS", "--require /app/.pnp.cjs"})
	}
	rt := Runtime{
		From:   build.From,
		Copies: []Copy{{From: "build", Src: []string{"/app/"}, Dst: "/app/", Exclude: nodeModulesExcludes(f)}},
		Env:    env,
		Cmd:    start,
	}
	if image, startEnv, cmd, check, ok := distrolessNode(f); ok {
		rt.From, rt.Cmd, rt.Env = image, cmd, append(rt.Env, startEnv...)
		if check != nil {
			build.Steps = append(build.Steps, *check)
		}
	}
	if f.Node.Manager == "bun" {
		if image, startEnv, cmd, ok := distrolessBun(f); ok {
			rt.From, rt.Cmd, rt.Env = image, cmd, append(rt.Env, startEnv...)
		}
	}
	if f.Workspace != nil {
		rt.Workdir = "/app/" + f.Workspace.App
	}
	return []Stage{build}, rt, nil
}
