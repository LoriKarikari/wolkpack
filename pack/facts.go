package pack

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

type Facts struct {
	Dir string
	// The day to check end-of-life dates against.
	Today     time.Time
	Workspace *Workspace
	// A uv workspace the Python app belongs to. Root is the build context.
	UvWorkspace *Workspace
	Node        *Node
	Python      *Python
	Ruby        *Ruby
	PHP         *PHP
	DotNet      *DotNet
	Rust        *Rust
	Go          *modfile.File
	GoCgo       bool
	Config      Config
	// From terril.toml, else the Procfile web: line.
	Start string
}

// A JS workspace the app belongs to. Root is the build context.
type Workspace struct {
	Root    string
	App     string
	Members map[string]Member
	// Every member's folder, relative to Root and sorted.
	Dirs []string
}

type Member struct {
	Dir   string
	Deps  []string
	Build bool
}

type Config struct {
	Provider      string   `toml:"provider"`
	Start         string   `toml:"start"`
	Packages      []string `toml:"packages"`
	BuildPackages []string `toml:"build-packages"`
}

type Node struct {
	Package        PackageJSON
	Manager        string
	ManagerVersion string
	Lockfile       string
	Version        string
	PnP            bool
	// Production bin names from package-lock.json, mapped to their file under node_modules.
	Bins map[string]string
}

type PackageJSON struct {
	Name            string            `json:"name"`
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	PackageManager  string            `json:"packageManager"`
	Engines         struct {
		Node string `json:"node"`
	} `json:"engines"`
}

type Python struct {
	Name         string
	Manager      string
	Requirements []string
	Deps         map[string]bool
	Version      string
}

type PHP struct {
	Composer bool
	// Package names from composer.lock.
	Packages map[string]bool
	// "public" for apps with public/index.php, "" for an index.php at the root.
	DocRoot string
}

type DotNet struct {
	// The project to publish, relative to the app.
	Project string
	// Every project file, relative to the app, so restore sees the projects it references.
	Projects        []string
	Assembly        string
	TargetFramework string
	Web             bool
	// The project turns off culture data, so the runtime can leave ICU out.
	Invariant bool
	// From global.json, when it pins one.
	SDKVersion string
}

type Rust struct {
	Package string
	Bin     string
	// The channel from rust-toolchain.toml, when it pins a version.
	Version string
}

type Ruby struct {
	Version string
	// Gem names from Gemfile.lock, including dependencies of dependencies.
	Gems map[string]bool
}

func (f *Facts) Has(name string) bool {
	_, err := os.Stat(filepath.Join(f.Dir, name))
	return err == nil
}

func (f *Facts) read(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(f.Dir, name))
	return string(b), err == nil
}

func (f *Facts) root() string {
	if f.Workspace != nil {
		return f.Workspace.Root
	}
	return f.Dir
}

func (f *Facts) rootHas(name string) bool {
	_, err := os.Stat(filepath.Join(f.root(), name))
	return err == nil
}

func (f *Facts) rootRead(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(f.root(), name))
	return string(b), err == nil
}

func (f *Facts) firstLine(names ...string) string {
	for _, n := range names {
		if s, ok := f.read(n); ok {
			return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
		}
	}
	return ""
}

func Read(dir string) (*Facts, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	f := &Facts{Dir: dir, Today: time.Now().UTC()}
	var err error
	if f.Has("package.json") {
		if f.Workspace, err = findWorkspace(dir); err != nil {
			return nil, err
		}
	}
	if f.Node, err = readNode(f); err != nil {
		return nil, err
	}
	if f.Python, err = readPython(f); err != nil {
		return nil, err
	}
	if f.Ruby, err = readRuby(f); err != nil {
		return nil, err
	}
	if f.PHP, err = readPHP(f); err != nil {
		return nil, err
	}
	if f.DotNet, err = readDotNet(f); err != nil {
		return nil, err
	}
	if f.Rust, err = readRust(f); err != nil {
		return nil, err
	}
	if f.Python != nil && f.Workspace == nil {
		if f.UvWorkspace, err = findUvWorkspace(dir); err != nil {
			return nil, err
		}
	}
	if ws := f.UvWorkspace; ws != nil {
		if !fileExists(filepath.Join(ws.Root, "uv.lock")) {
			return nil, fmt.Errorf("the uv workspace at %s has no uv.lock; run uv lock there", ws.Root)
		}
		if f.Python.Name == "" {
			return nil, fmt.Errorf("%s/pyproject.toml has no project name, which uv needs to select it", ws.App)
		}
		f.Python.Manager = "uv"
		if f.Python.Version == "" {
			f.Python.Version = (&Facts{Dir: ws.Root}).firstLine(".python-version")
		}
	}
	if s, ok := f.read("go.mod"); ok {
		if f.Go, err = modfile.Parse("go.mod", []byte(s), nil); err != nil {
			return nil, err
		}
		if f.GoCgo, err = goUsesCgo(f); err != nil {
			return nil, err
		}
	}
	if s, ok := f.read("Procfile"); ok {
		f.Start = procfileWeb(s)
	}
	if f.Has("wolkpack.toml") {
		return nil, fmt.Errorf("wolkpack.toml is now terril.toml; rename the file")
	}
	if s, ok := f.read("terril.toml"); ok {
		if f.Config, err = parseConfig(s); err != nil {
			return nil, err
		}
	}
	if f.Config.Start != "" {
		f.Start = f.Config.Start
	}
	return f, nil
}

var nodeLockfiles = []struct{ file, manager string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"package-lock.json", "npm"},
	{"yarn.lock", "yarn"},
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
}

func readNode(f *Facts) (*Node, error) {
	s, ok := f.read("package.json")
	if !ok {
		return nil, nil
	}
	n := &Node{}
	var err error
	if err := json.Unmarshal([]byte(s), &n.Package); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	for _, l := range nodeLockfiles {
		if f.rootHas(l.file) {
			n.Manager, n.Lockfile = l.manager, l.file
			break
		}
	}
	pm := n.Package.PackageManager
	if rootPkg, ok := f.rootRead("package.json"); ok && pm == "" && f.Workspace != nil {
		var root PackageJSON
		if err := json.Unmarshal([]byte(rootPkg), &root); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(f.root(), "package.json"), err)
		}
		pm = root.PackageManager
	}
	// packageManager looks like "pnpm@10.4.1+sha512.abc".
	if pm != "" {
		name, version, _ := strings.Cut(strings.SplitN(pm, "+", 2)[0], "@")
		if n.Manager == "" {
			n.Manager = name
		}
		if name == n.Manager {
			n.ManagerVersion = version
		}
	}
	if n.Manager == "" {
		n.Manager = "npm"
	}
	if n.Lockfile == "package-lock.json" && f.Workspace == nil {
		lock, _ := f.read("package-lock.json")
		if n.Bins, err = lockBins(lock); err != nil {
			return nil, err
		}
	}
	if n.Manager == "yarn" {
		lock, _ := f.rootRead("yarn.lock")
		yarnrc, _ := f.rootRead(".yarnrc.yml")
		if f.rootHas(".yarnrc.yml") || strings.Contains(lock, "__metadata:") || !strings.HasPrefix(n.ManagerVersion+"1", "1") {
			n.Manager = "yarn-berry"
			n.PnP = !nodeLinkerRe.MatchString(yarnrc)
		}
	}
	n.Version = strings.TrimPrefix(f.firstLine(".nvmrc", ".node-version"), "v")
	if n.Version == "" && f.Workspace != nil {
		root := &Facts{Dir: f.Workspace.Root}
		n.Version = strings.TrimPrefix(root.firstLine(".nvmrc", ".node-version"), "v")
	}
	if n.Version == "" || strings.HasPrefix(n.Version, "lts") {
		n.Version = n.Package.Engines.Node
	}
	return n, nil
}

// findWorkspace walks up from an app to the JS workspace that lists it, stopping at the repo root.
func findWorkspace(dir string) (*Workspace, error) {
	app, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for parent := filepath.Dir(app); ; parent = filepath.Dir(parent) {
		patterns, err := workspacePatterns(parent)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(parent, app)
		rel = filepath.ToSlash(rel)
		if inWorkspace(patterns, rel) {
			members, err := readMembers(parent, patterns)
			var dirs []string
			for _, m := range members {
				dirs = append(dirs, m.Dir)
			}
			slices.Sort(dirs)
			return &Workspace{Root: parent, App: rel, Members: members, Dirs: dirs}, err
		}
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil || parent == filepath.Dir(parent) {
			return nil, nil
		}
	}
}

func inWorkspace(patterns []string, rel string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, rel); ok || strings.HasSuffix(p, "/**") && strings.HasPrefix(rel, strings.TrimSuffix(p, "**")) {
			return true
		}
	}
	return false
}

func readMembers(root string, patterns []string) (map[string]Member, error) {
	members := map[string]Member{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.Name() != "package.json" || p == filepath.Join(root, "package.json") {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if !inWorkspace(patterns, rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var pkg struct {
			Name            string            `json:"name"`
			Scripts         map[string]string `json:"scripts"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(b, &pkg); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		m := Member{Dir: rel, Build: pkg.Scripts["build"] != ""}
		for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
			for name := range deps {
				m.Deps = append(m.Deps, name)
			}
		}
		slices.Sort(m.Deps)
		members[pkg.Name] = m
		return nil
	})
	return members, err
}

// findUvWorkspace walks up from an app to the uv workspace that lists it, stopping at the repo root.
func findUvWorkspace(dir string) (*Workspace, error) {
	app, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for parent := filepath.Dir(app); ; parent = filepath.Dir(parent) {
		if b, err := os.ReadFile(filepath.Join(parent, "pyproject.toml")); err == nil {
			var doc struct {
				Tool struct {
					Uv struct {
						Workspace struct {
							Members []string `toml:"members"`
							Exclude []string `toml:"exclude"`
						} `toml:"workspace"`
					} `toml:"uv"`
				} `toml:"tool"`
			}
			if _, err := toml.Decode(string(b), &doc); err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Join(parent, "pyproject.toml"), err)
			}
			ws := doc.Tool.Uv.Workspace
			rel, _ := filepath.Rel(parent, app)
			rel = filepath.ToSlash(rel)
			if inWorkspace(ws.Members, rel) && !inWorkspace(ws.Exclude, rel) {
				dirs, err := uvMemberDirs(parent, ws.Members, ws.Exclude)
				return &Workspace{Root: parent, App: rel, Dirs: dirs}, err
			}
		}
		if fileExists(filepath.Join(parent, ".git")) || parent == filepath.Dir(parent) {
			return nil, nil
		}
	}
}

func uvMemberDirs(root string, members, exclude []string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && p != root || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Name() != "pyproject.toml" || p == filepath.Join(root, "pyproject.toml") {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if inWorkspace(members, rel) && !inWorkspace(exclude, rel) {
			dirs = append(dirs, rel)
		}
		return nil
	})
	slices.Sort(dirs)
	return dirs, err
}

func workspacePatterns(dir string) ([]string, error) {
	if b, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		var ws struct {
			Packages []string `yaml:"packages"`
		}
		if err := yaml.Unmarshal(b, &ws); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "pnpm-workspace.yaml"), err)
		}
		return ws.Packages, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, nil
	}
	var pkg struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "package.json"), err)
	}
	if len(pkg.Workspaces) == 0 {
		return nil, nil
	}
	var patterns []string
	if json.Unmarshal(pkg.Workspaces, &patterns) != nil {
		var yarnClassic struct {
			Packages []string `json:"packages"`
		}
		if err := json.Unmarshal(pkg.Workspaces, &yarnClassic); err != nil {
			return nil, fmt.Errorf("%s: workspaces must be a list or have a packages list", filepath.Join(dir, "package.json"))
		}
		patterns = yarnClassic.Packages
	}
	return patterns, nil
}

// Modules that need cgo. A miss fails the build with the package's name, so it can be added here.
var cgoModules = []string{
	"github.com/mattn/go-sqlite3",
	"github.com/confluentinc/confluent-kafka-go",
	"github.com/confluentinc/confluent-kafka-go/v2",
}

var importCRe = regexp.MustCompile(`(?m)^\s*(import\s+)?"C"\s*$`)

func goUsesCgo(f *Facts) (bool, error) {
	for _, r := range f.Go.Require {
		if slices.Contains(cgoModules, r.Mod.Path) {
			return true, nil
		}
	}
	found := false
	err := filepath.WalkDir(f.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != f.Dir && (name == "vendor" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || fileExists(filepath.Join(p, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		found = err == nil && importCRe.Match(b)
		return err
	})
	return found, err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func lockBins(lock string) (map[string]string, error) {
	var doc struct {
		Packages map[string]struct {
			Bin map[string]string `json:"bin"`
			Dev bool              `json:"dev"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(lock), &doc); err != nil {
		return nil, fmt.Errorf("package-lock.json: %w", err)
	}
	bins := map[string]string{}
	for key, pkg := range doc.Packages {
		dir, top := strings.CutPrefix(key, "node_modules/")
		if !top || pkg.Dev || strings.Contains(dir, "/node_modules/") {
			continue
		}
		for name, file := range pkg.Bin {
			bins[name] = path.Join(key, file)
		}
	}
	return bins, nil
}

var (
	lockGemRe         = regexp.MustCompile(`(?m)^    ([A-Za-z0-9_.-]+) \(`)
	lockSectionRe     = regexp.MustCompile(`(?m)^([A-Z][A-Z ]*)\n((?:  .*\n?)*)`)
	gemfileRubyRe     = regexp.MustCompile(`(?m)^\s*ruby\s+["']([^"']+)["']`)
	rubyVersionNumber = regexp.MustCompile(`\d+(\.\d+){0,2}`)
)

func readPHP(f *Facts) (*PHP, error) {
	p := &PHP{Packages: map[string]bool{}, Composer: f.Has("composer.json")}
	switch {
	case f.Has("public/index.php"):
		p.DocRoot = "public"
	case f.Has("index.php"):
	case p.Composer:
		return nil, fmt.Errorf("composer.json found, but no index.php in public/ or the app root")
	default:
		return nil, nil
	}
	if !p.Composer {
		return p, nil
	}
	lock, ok := f.read("composer.lock")
	if !ok {
		return nil, fmt.Errorf("composer.json has no composer.lock; run composer update and commit it")
	}
	var doc struct {
		Packages []struct {
			Name string `json:"name"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(lock), &doc); err != nil {
		return nil, fmt.Errorf("composer.lock: %w", err)
	}
	for _, pkg := range doc.Packages {
		p.Packages[pkg.Name] = true
	}
	return p, nil
}

func readRuby(f *Facts) (*Ruby, error) {
	if !f.Has("Gemfile") {
		return nil, nil
	}
	lock, ok := f.read("Gemfile.lock")
	if !ok {
		return nil, fmt.Errorf("no Gemfile.lock next to the Gemfile; run bundle lock and commit it")
	}
	r := &Ruby{Gems: map[string]bool{}}
	for _, m := range lockGemRe.FindAllStringSubmatch(lock, -1) {
		r.Gems[m[1]] = true
	}
	sections := map[string][]string{}
	for _, m := range lockSectionRe.FindAllStringSubmatch(lock, -1) {
		sections[m[1]] = strings.Fields(m[2])
	}
	platforms := sections["PLATFORMS"]
	if !slices.ContainsFunc(platforms, func(p string) bool { return p == "ruby" || strings.Contains(p, "-linux") }) {
		return nil, fmt.Errorf("the Gemfile.lock only lists %s; run bundle lock --add-platform x86_64-linux aarch64-linux",
			strings.Join(platforms, ", "))
	}
	if v := f.firstLine(".ruby-version"); v != "" {
		r.Version = rubyVersionNumber.FindString(v)
	} else if v := sections["RUBY VERSION"]; len(v) > 1 {
		r.Version = rubyVersionNumber.FindString(v[1])
	} else if gemfile, _ := f.read("Gemfile"); gemfileRubyRe.MatchString(gemfile) {
		r.Version = gemfileRubyRe.FindStringSubmatch(gemfile)[1]
	}
	return r, nil
}

var nodeLinkerRe = regexp.MustCompile(`(?m)^nodeLinker:\s*["']?(node-modules|pnpm)`)

func readPython(f *Facts) (*Python, error) {
	py := &Python{Manager: "pip", Deps: map[string]bool{}}
	found := false
	if s, ok := f.read("requirements.txt"); ok {
		found = true
		for _, name := range requirementNames(s) {
			py.Deps[name] = true
		}
	}
	if s, ok := f.read("pyproject.toml"); ok {
		found = true
		var doc struct {
			Project struct {
				Name           string   `toml:"name"`
				Dependencies   []string `toml:"dependencies"`
				RequiresPython string   `toml:"requires-python"`
			} `toml:"project"`
			Tool struct {
				Poetry struct {
					Dependencies map[string]any `toml:"dependencies"`
				} `toml:"poetry"`
			} `toml:"tool"`
		}
		if _, err := toml.Decode(s, &doc); err != nil {
			return nil, fmt.Errorf("pyproject.toml: %w", err)
		}
		py.Name = doc.Project.Name
		py.Requirements = doc.Project.Dependencies
		for _, spec := range doc.Project.Dependencies {
			py.Deps[distName(spec)] = true
		}
		for name := range doc.Tool.Poetry.Dependencies {
			py.Deps[distName(name)] = true
		}
		py.Version = doc.Project.RequiresPython
	}
	if !found {
		return nil, nil
	}
	switch {
	case f.Has("uv.lock"):
		py.Manager = "uv"
	case f.Has("poetry.lock"):
		py.Manager = "poetry"
	}
	if v := f.firstLine(".python-version"); v != "" {
		py.Version = v
	}
	return py, nil
}

var distNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
var normalizeRe = regexp.MustCompile(`[-_.]+`)

// PEP 503 normalization, so "Django_Rest" matches "django-rest".
func distName(spec string) string {
	return strings.ToLower(normalizeRe.ReplaceAllString(distNameRe.FindString(strings.TrimSpace(spec)), "-"))
}

func requirementNames(s string) []string {
	var names []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		if name := distName(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

var aptPackageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)

func parseConfig(s string) (Config, error) {
	var c Config
	md, err := toml.Decode(s, &c)
	if err != nil {
		return c, fmt.Errorf("terril.toml: %w", err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return c, fmt.Errorf("terril.toml: unknown key %q (have: provider, start, packages, build-packages)", keys[0].String())
	}
	for _, p := range append(c.Packages, c.BuildPackages...) {
		if !aptPackageRe.MatchString(p) {
			return c, fmt.Errorf("terril.toml: %q isn't a Debian package name", p)
		}
	}
	return c, nil
}

// Distroless images have no shell for a custom start command and no apt for packages.
func (f *Facts) needsDebianRuntime() bool {
	return f.Start != "" || len(f.Config.Packages) > 0
}

func procfileWeb(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if cmd, ok := strings.CutPrefix(strings.TrimSpace(line), "web:"); ok {
			return strings.TrimSpace(cmd)
		}
	}
	return ""
}

type csproj struct {
	SDK    string `xml:"Sdk,attr"`
	Groups []struct {
		TargetFramework        string `xml:"TargetFramework"`
		TargetFrameworks       string `xml:"TargetFrameworks"`
		AssemblyName           string `xml:"AssemblyName"`
		InvariantGlobalization string `xml:"InvariantGlobalization"`
	} `xml:"PropertyGroup"`
}

func readDotNet(f *Facts) (*DotNet, error) {
	var projects []string
	err := filepath.WalkDir(f.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != f.Dir && (d.Name() == "bin" || d.Name() == "obj" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if strings.HasSuffix(d.Name(), ".csproj") {
			rel, _ := filepath.Rel(f.Dir, p)
			projects = append(projects, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil || len(projects) == 0 {
		return nil, err
	}
	slices.Sort(projects)

	parsed := map[string]csproj{}
	var web []string
	for _, p := range projects {
		s, _ := f.read(p)
		var c csproj
		if err := xml.Unmarshal([]byte(s), &c); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		parsed[p] = c
		if c.SDK == "Microsoft.NET.Sdk.Web" {
			web = append(web, p)
		}
	}
	// The project at the app root wins. Otherwise the only web project does, so a solution with
	// one API and its test projects builds the API.
	var project string
	for _, p := range projects {
		if !strings.Contains(p, "/") {
			if project != "" {
				return nil, fmt.Errorf("several .NET projects at the app root (%s); keep one", strings.Join(projects, ", "))
			}
			project = p
		}
	}
	switch {
	case project != "":
	case len(web) == 1:
		project = web[0]
	case len(web) > 1:
		return nil, fmt.Errorf("several .NET web projects (%s); point terril at the folder of the one to build", strings.Join(web, ", "))
	case len(projects) == 1:
		project = projects[0]
	default:
		return nil, fmt.Errorf("several .NET projects (%s); point terril at the folder of the one to build", strings.Join(projects, ", "))
	}

	c := parsed[project]
	d := &DotNet{
		Project:  project,
		Projects: projects,
		Assembly: strings.TrimSuffix(path.Base(project), ".csproj"),
		Web:      c.SDK == "Microsoft.NET.Sdk.Web",
	}
	for _, g := range c.Groups {
		if g.AssemblyName != "" {
			d.Assembly = g.AssemblyName
		}
		if strings.EqualFold(g.InvariantGlobalization, "true") {
			d.Invariant = true
		}
		if g.TargetFramework != "" {
			d.TargetFramework = g.TargetFramework
		} else if g.TargetFrameworks != "" && d.TargetFramework == "" {
			frameworks := strings.Split(g.TargetFrameworks, ";")
			d.TargetFramework = strings.TrimSpace(frameworks[len(frameworks)-1])
		}
	}
	if s, ok := f.read("global.json"); ok {
		var g struct {
			SDK struct {
				Version string `json:"version"`
			} `json:"sdk"`
		}
		if err := json.Unmarshal([]byte(s), &g); err != nil {
			return nil, fmt.Errorf("global.json: %w", err)
		}
		d.SDKVersion = g.SDK.Version
	}
	return d, nil
}

type cargoToml struct {
	Package *struct {
		Name       string `toml:"name"`
		DefaultRun string `toml:"default-run"`
		Autobins   *bool  `toml:"autobins"`
	} `toml:"package"`
	Bin []struct {
		Name string `toml:"name"`
	} `toml:"bin"`
	Workspace *struct {
		Members []string `toml:"members"`
	} `toml:"workspace"`
}

func readRust(f *Facts) (*Rust, error) {
	root, ok := f.read("Cargo.toml")
	if !ok {
		return nil, nil
	}
	if !f.Has("Cargo.lock") {
		return nil, fmt.Errorf("no Cargo.lock next to Cargo.toml; run cargo generate-lockfile and commit it")
	}
	var manifest cargoToml
	if _, err := toml.Decode(root, &manifest); err != nil {
		return nil, fmt.Errorf("reading Cargo.toml: %w", err)
	}
	dirs := []string{"."}
	if manifest.Workspace != nil {
		for _, pattern := range manifest.Workspace.Members {
			matches, _ := filepath.Glob(filepath.Join(f.Dir, pattern, "Cargo.toml"))
			for _, m := range matches {
				rel, _ := filepath.Rel(f.Dir, filepath.Dir(m))
				dirs = append(dirs, filepath.ToSlash(rel))
			}
		}
	}

	type bin struct{ pkg, name string }
	var bins []bin
	for _, dir := range dirs {
		var c cargoToml
		if dir == "." {
			c = manifest
		} else {
			s, _ := f.read(path.Join(dir, "Cargo.toml"))
			if _, err := toml.Decode(s, &c); err != nil {
				return nil, fmt.Errorf("%s/Cargo.toml: %w", dir, err)
			}
		}
		if c.Package == nil {
			continue
		}
		names := map[string]bool{}
		for _, b := range c.Bin {
			names[b.Name] = true
		}
		// Cargo finds binaries in src/main.rs and src/bin/ unless autobins is off.
		if c.Package.Autobins == nil || *c.Package.Autobins {
			if f.Has(path.Join(dir, "src/main.rs")) {
				names[c.Package.Name] = true
			}
			entries, _ := os.ReadDir(filepath.Join(f.Dir, dir, "src/bin"))
			for _, e := range entries {
				if name, ok := strings.CutSuffix(e.Name(), ".rs"); ok && !e.IsDir() {
					names[name] = true
				} else if e.IsDir() && f.Has(path.Join(dir, "src/bin", e.Name(), "main.rs")) {
					names[e.Name()] = true
				}
			}
		}
		if dir == "." && c.Package.DefaultRun != "" {
			return &Rust{Package: c.Package.Name, Bin: c.Package.DefaultRun, Version: rustToolchain(f)}, nil
		}
		for name := range names {
			bins = append(bins, bin{c.Package.Name, name})
		}
	}
	switch len(bins) {
	case 0:
		return nil, fmt.Errorf("no Rust binary: add src/main.rs or a [[bin]] to Cargo.toml")
	case 1:
		return &Rust{Package: bins[0].pkg, Bin: bins[0].name, Version: rustToolchain(f)}, nil
	}
	var names []string
	for _, b := range bins {
		names = append(names, b.name)
	}
	slices.Sort(names)
	return nil, fmt.Errorf("several Rust binaries (%s); set default-run in Cargo.toml", strings.Join(names, ", "))
}

func rustToolchain(f *Facts) string {
	if s, ok := f.read("rust-toolchain.toml"); ok {
		var t struct {
			Toolchain struct {
				Channel string `toml:"channel"`
			} `toml:"toolchain"`
		}
		if _, err := toml.Decode(s, &t); err == nil {
			return t.Toolchain.Channel
		}
	}
	return f.firstLine("rust-toolchain")
}
