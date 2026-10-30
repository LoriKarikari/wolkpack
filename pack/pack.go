package pack

import (
	"fmt"
	"slices"
	"strings"
)

type Role uint8

const (
	Primary Role = 1 << iota
	Assets
)

type provider struct {
	name       string
	detect     func(*Facts) Role
	plan       func(f *Facts, assetStages []string) ([]Stage, Runtime, error)
	assetStage func(f *Facts, name string) (Stage, error)
	// The runtime the image ships, for end-of-life warnings.
	versions *runtimeVersions
	version  func(*Facts) (string, error)
}

var providers = []provider{goProvider, rustProvider, pythonProvider, rubyProvider, phpProvider, dotnetProvider, nodeProvider, staticProvider}

func New(f *Facts, forceProvider string) (*Plan, error) {
	roles := map[string]Role{}
	for _, p := range providers {
		roles[p.name] = p.detect(f)
	}

	if forceProvider == "" {
		forceProvider = f.Config.Provider
	}
	primary, err := pickPrimary(roles, forceProvider)
	if err != nil {
		return nil, err
	}

	plan := &Plan{Provider: primary.name, Context: f.Dir}
	if f.Workspace != nil {
		if primary.name != "node" {
			return nil, fmt.Errorf("%s is in the JS workspace at %s, but builds as %s; workspaces only support node apps", f.Dir, f.Workspace.Root, primary.name)
		}
		plan.Context = f.Workspace.Root
	}
	if ws := f.UvWorkspace; ws != nil {
		if primary.name != "python" {
			return nil, fmt.Errorf("%s is in the uv workspace at %s, but builds as %s", f.Dir, ws.Root, primary.name)
		}
		if roles["node"]&Assets != 0 {
			return nil, fmt.Errorf("%s builds Node assets, which terril doesn't support inside a uv workspace yet", f.Dir)
		}
		plan.Context = ws.Root
	}
	var assetNames []string
	for _, p := range providers {
		if p.name == primary.name || roles[p.name]&Assets == 0 {
			continue
		}
		name := "assets-" + p.name
		s, err := p.assetStage(f, name)
		if err != nil {
			return nil, err
		}
		plan.Assets = append(plan.Assets, p.name)
		plan.Stages = append(plan.Stages, s)
		assetNames = append(assetNames, name)
	}

	stages, rt, err := primary.plan(f, assetNames)
	if err != nil {
		return nil, err
	}
	plan.Stages = append(plan.Stages, stages...)
	plan.Runtime = rt
	for _, p := range providers {
		if p.version == nil || p.name != primary.name && !slices.Contains(plan.Assets, p.name) {
			continue
		}
		v, err := p.version(f)
		if err != nil {
			return nil, err
		}
		if w := p.versions.warning(v, f.Today); w != "" {
			plan.Warnings = append(plan.Warnings, w)
		}
	}
	if f.Start != "" {
		plan.Runtime.Cmd = Cmd{"sh", "-c", f.Start}
	}
	if build := append(f.Config.Packages, f.Config.BuildPackages...); len(build) > 0 {
		for i := range plan.Stages {
			plan.Stages[i].Steps = append([]Step{aptInstall(build)}, plan.Stages[i].Steps...)
		}
	}
	plan.Runtime.Packages = f.Config.Packages
	return plan, nil
}

func pickPrimary(roles map[string]Role, force string) (provider, error) {
	if force != "" {
		for _, p := range providers {
			if p.name == force {
				return p, nil
			}
		}
		return provider{}, fmt.Errorf("unknown provider %q (have: %s)", force, strings.Join(providerNames(), ", "))
	}

	var candidates []provider
	for _, p := range providers {
		if roles[p.name]&Primary != 0 {
			candidates = append(candidates, p)
		}
	}
	// So a Python app with a package.json runs as Python, with Node building its assets.
	if slices.ContainsFunc(candidates, func(p provider) bool { return roles[p.name]&Assets == 0 }) {
		candidates = slices.DeleteFunc(candidates, func(p provider) bool { return roles[p.name]&Assets != 0 })
	}

	switch len(candidates) {
	case 0:
		return provider{}, fmt.Errorf("no package.json, requirements.txt, pyproject.toml, Gemfile, composer.json, index.php, *.csproj, Cargo.toml, go.mod or index.html found; don't know how to build this app")
	case 1:
		return candidates[0], nil
	}
	var names []string
	for _, p := range candidates {
		names = append(names, p.name)
	}
	return provider{}, fmt.Errorf("both %s can run this app; pick one with --provider %s",
		strings.Join(names, " and "), strings.Join(names, "|"))
}

func providerNames() []string {
	var names []string
	for _, p := range providers {
		names = append(names, p.name)
	}
	return names
}
