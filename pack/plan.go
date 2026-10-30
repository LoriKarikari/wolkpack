package pack

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Fixed so volume permissions are predictable.
const appUser = "1000:1000"

var sourceExcludes = []string{"**/.git", "**/node_modules", "**/.venv", "**/__pycache__"}

type Plan struct {
	// The directory docker builds from.
	Context  string
	Provider string
	Assets   []string
	Stages   []Stage
	Runtime  Runtime
	// Names of variables the build steps read. Their values never reach the image.
	BuildEnv []string
	Warnings []string
}

type Runtime struct {
	Workdir  string
	From     string
	Packages []string
	Copies   []Copy
	Env      []Env
	Files    []File
	Cmd      Cmd
}

type Stage struct {
	Name    string
	Workdir string
	From    string
	Steps   []Step
}

type Step interface{ dockerfile() string }

type Run struct {
	Cmd     string
	Caches  []string
	Secrets []string
}

type Arg string

type Copy struct {
	From    string
	Src     []string
	Dst     string
	Exclude []string
	// Keeps each source's path under Dst, so manifests land in their member's folder.
	Parents bool
	Chown   bool
	// Keeps root as the owner in the runtime image, so the app can't modify these files.
	RootOwned bool
}

type Env struct{ Key, Value string }

type File struct {
	Path, Content string
	Executable    bool
}

// Add downloads a file and checks its sha256 before it lands in the stage.
type Add struct {
	URL, SHA256, Dst string
	Executable       bool
}

type User string

type ClearEntrypoint struct{}

type Cmd []string

func (r Run) dockerfile() string {
	var b strings.Builder
	b.WriteString("RUN ")
	for _, c := range r.Caches {
		fmt.Fprintf(&b, "--mount=type=cache,target=%s ", c)
	}
	for _, s := range r.Secrets {
		fmt.Fprintf(&b, "--mount=type=secret,id=%s,env=%s ", s, s)
	}
	b.WriteString(r.Cmd)
	return b.String()
}

func (c Copy) dockerfile() string {
	var b strings.Builder
	b.WriteString("COPY ")
	if c.From != "" {
		fmt.Fprintf(&b, "--from=%s ", c.From)
	}
	if c.Chown {
		fmt.Fprintf(&b, "--chown=%s ", appUser)
	}
	for _, e := range c.Exclude {
		fmt.Fprintf(&b, "--exclude=%s ", e)
	}
	if c.Parents {
		b.WriteString("--parents ")
	}
	b.WriteString(strings.Join(c.Src, " ") + " " + c.Dst)
	return b.String()
}

func (f File) dockerfile() string {
	chmod := ""
	if f.Executable {
		chmod = "--chmod=755 "
	}
	return "COPY " + chmod + "<<'EOF' " + f.Path + "\n" + f.Content + "EOF"
}

func (a Add) dockerfile() string {
	chmod := ""
	if a.Executable {
		chmod = "--chmod=755 "
	}
	return "ADD " + chmod + "--checksum=sha256:" + a.SHA256 + " " + a.URL + " " + a.Dst
}

func (a Arg) dockerfile() string { return "ARG " + string(a) }

func (e Env) dockerfile() string  { return fmt.Sprintf("ENV %s=%q", e.Key, e.Value) }
func (u User) dockerfile() string { return "USER " + string(u) }

func (ClearEntrypoint) dockerfile() string { return "ENTRYPOINT []" }

func (c Cmd) dockerfile() string {
	j, _ := json.Marshal([]string(c))
	return "CMD " + string(j)
}

func copySource() Copy { return Copy{Src: []string{"."}, Dst: "./", Exclude: sourceExcludes} }

func copyAssets(stages []string) []Step {
	var steps []Step
	for _, s := range stages {
		steps = append(steps, Copy{From: s, Src: []string{"/app/"}, Dst: "./", Exclude: []string{"node_modules"}})
	}
	return steps
}

func (r Runtime) stage() Stage {
	var steps []Step
	if len(r.Packages) > 0 {
		steps = append(steps, aptInstall(r.Packages))
	}
	steps = append(steps, Env{"PORT", "8080"})
	for _, e := range r.Env {
		steps = append(steps, e)
	}
	for _, f := range r.Files {
		steps = append(steps, f)
	}
	for _, c := range r.Copies {
		c.Chown = !c.RootOwned
		steps = append(steps, c)
	}
	steps = append(steps, User(appUser), ClearEntrypoint{}, r.Cmd)
	return Stage{Name: "runtime", From: r.From, Workdir: r.Workdir, Steps: steps}
}

// The package lists live in a cache mount, so they never reach a layer. dpkg and debconf
// keep -old copies and logs of every change, which the same step deletes.
func aptInstall(packages []string) Run {
	return Run{
		Cmd: "apt-get update && apt-get install -y --no-install-recommends " + strings.Join(packages, " ") +
			" && rm -rf /var/log/apt /var/log/dpkg.log /var/cache/debconf/*-old /var/lib/dpkg/*-old",
		Caches: []string{"/var/lib/apt/lists,sharing=locked"},
	}
}

func (p *Plan) Dockerfile() string {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	fmt.Fprintf(&b, "# terril: %s\n", p.Summary())
	all := append(slices.Clone(p.Stages), p.Runtime.stage())
	if slices.ContainsFunc(all, func(s Stage) bool { return strings.Contains(s.From, "${TARGETARCH}") }) {
		b.WriteString("\nARG TARGETARCH\n")
	}
	stages := make([]Stage, len(p.Stages))
	for i, s := range p.Stages {
		s.Steps = withBuildEnv(s.Steps, p.BuildEnv)
		stages[i] = s
	}
	for _, s := range append(stages, p.Runtime.stage()) {
		workdir := "/app"
		if s.Workdir != "" {
			workdir = s.Workdir
		}
		fmt.Fprintf(&b, "\nFROM %s AS %s\nWORKDIR %s\n", s.From, s.Name, workdir)
		for _, step := range s.Steps {
			b.WriteString(step.dockerfile() + "\n")
		}
	}
	return b.String()
}

// BuildEnvArg changes when a build env value changes, so Docker reruns the steps that read it.
const BuildEnvArg = "TERRIL_BUILD_ENV"

// Secret mounts don't invalidate Docker's cache, so BuildEnvArg goes in right after the source copy.
func withBuildEnv(steps []Step, names []string) []Step {
	if len(names) == 0 {
		return steps
	}
	out := make([]Step, 0, len(steps)+1)
	argAt := 0
	for i, step := range steps {
		if c, ok := step.(Copy); ok && c.From == "" && len(c.Src) == 1 && c.Src[0] == "." {
			argAt = i + 1
		}
	}
	for i, step := range steps {
		if i == argAt {
			out = append(out, Arg(BuildEnvArg))
		}
		if r, ok := step.(Run); ok {
			r.Secrets = names
			step = r
		}
		out = append(out, step)
	}
	if argAt == len(steps) {
		out = append(out, Arg(BuildEnvArg))
	}
	return out
}

func (p *Plan) Summary() string {
	if len(p.Assets) == 0 {
		return p.Provider
	}
	return p.Provider + " + assets from " + strings.Join(p.Assets, ", ")
}
