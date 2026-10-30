package pack

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var pythonProvider = provider{
	name: "python",
	detect: func(f *Facts) Role {
		if f.Python == nil {
			return 0
		}
		return Primary
	},
	plan:     planPython,
	versions: &pythonVersions,
	version:  pythonVersion,
}

const venv = "/app/.venv"

const pythonDir = "/python"

// renovate: datasource=docker depName=ghcr.io/astral-sh/uv
const uvVersion = "0.13.5"

// renovate: datasource=pypi depName=poetry
const poetryVersion = "2.5.1"

// Parts of uv's Python a server never uses. The bin/python3.x executable embeds the
// interpreter, so libpython is only for programs that embed Python themselves.
var pythonRuntimeExcludes = []string{
	"*/lib/libpython3*",
	"*/lib/python3*/site-packages/pip*",
	"*/lib/python3*/ensurepip",
	"*/lib/python3*/idlelib",
	"*/lib/python3*/tkinter",
	"*/lib/python3*/turtledemo",
	"*/lib/python3*/lib-dynload/_tkinter*",
	"*/lib/python3*/config-3*",
	"*/lib/tcl*",
	"*/lib/tk*",
	"*/lib/libtcl*",
	"*/lib/libtk*",
	"*/lib/itcl*",
	"*/lib/thread*",
	"*/include",
	"*/share",
	"*/bin/idle*",
	"*/bin/pip*",
}

func pythonVersion(f *Facts) (string, error) {
	return pythonVersions.pick(f.Python.Version, f.Today)
}

var venvPath = Env{"PATH", venv + "/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}

func planPython(f *Facts, assets []string) ([]Stage, Runtime, error) {
	py := f.Python
	v, err := pythonVersion(f)
	if err != nil {
		return nil, Runtime{}, err
	}
	uvCache := []string{"/root/.cache/uv"}
	steps := []Step{
		venvPath,
		Copy{From: "ghcr.io/astral-sh/uv:" + uvVersion, Src: []string{"/uv"}, Dst: "/usr/local/bin/uv"},
		Env{"UV_PYTHON", v},
		Env{"UV_PROJECT_ENVIRONMENT", venv},
		Env{"VIRTUAL_ENV", venv},
		Env{"UV_LINK_MODE", "copy"},
		Env{"UV_COMPILE_BYTECODE", "1"},
	}
	// uv's standalone Python needs only glibc, so the runtime can be distroless. The venv
	// links to /python by absolute path, so both stages keep it there. A shell or apt packages
	// need Debian, where python:slim's Python is smaller because it shares Debian's libraries.
	debian := f.needsDebianRuntime()
	buildImage, runtimeImage := "debian:trixie-slim", "gcr.io/distroless/cc-debian13"
	if debian {
		buildImage = "python:" + v + "-slim"
		runtimeImage = buildImage
		steps = append(steps, Env{"UV_PYTHON_PREFERENCE", "only-system"})
	} else {
		steps = append(steps,
			Env{"UV_PYTHON_INSTALL_DIR", pythonDir},
			Env{"UV_PYTHON_CACHE_DIR", "/root/.cache/uv/python"},
			Env{"UV_PYTHON_PREFERENCE", "only-managed"},
			Run{Cmd: "uv python install " + v, Caches: uvCache},
		)
	}
	switch py.Manager {
	case "uv":
		if ws := f.UvWorkspace; ws != nil {
			// Dependencies install from the manifests alone; members need their source to build.
			manifests := []string{"pyproject.toml", "uv.lock"}
			for _, dir := range ws.Dirs {
				manifests = append(manifests, dir+"/pyproject.toml")
			}
			sync := "uv sync --frozen --no-dev --package " + shellQuote([]string{py.Name})
			steps = append(steps,
				Copy{Src: manifests, Dst: "./", Parents: true},
				Run{Cmd: sync + " --no-install-workspace", Caches: uvCache},
				copySource(),
				Run{Cmd: sync, Caches: uvCache},
			)
		} else {
			steps = append(steps,
				Copy{Src: []string{"pyproject.toml", "uv.lock"}, Dst: "./"},
				Run{Cmd: "uv sync --frozen --no-dev --no-install-project", Caches: uvCache},
				copySource(),
			)
		}
	case "pip":
		// uv venv leaves pip out of the image; uv pip installs the same requirements.
		steps = append(steps, Run{Cmd: "uv venv " + venv})
		if f.Has("requirements.txt") {
			steps = append(steps,
				Copy{Src: []string{"requirements.txt"}, Dst: "./"},
				Run{Cmd: "uv pip install -r requirements.txt", Caches: uvCache})
		} else if len(py.Requirements) > 0 {
			steps = append(steps, Run{Cmd: "uv pip install " + shellQuote(py.Requirements), Caches: uvCache})
		}
	case "poetry":
		steps = append(steps,
			Run{Cmd: "uv venv " + venv},
			Env{"UV_TOOL_BIN_DIR", "/usr/local/bin"},
			Run{Cmd: "uv tool install poetry==" + poetryVersion, Caches: uvCache},
			Env{"POETRY_VIRTUALENVS_IN_PROJECT", "true"},
			Env{"POETRY_NO_INTERACTION", "1"},
			Copy{Src: []string{"pyproject.toml", "poetry.lock"}, Dst: "./"},
			Run{Cmd: "poetry install --only main --no-root", Caches: []string{"/root/.cache/pypoetry"}},
		)
	}
	if py.Manager != "uv" {
		steps = append(steps, copySource())
	}
	steps = append(steps, copyAssets(assets)...)
	if djangoCollectsStatic(f) {
		cmd := "python manage.py collectstatic --noinput"
		if ws := f.UvWorkspace; ws != nil {
			cmd = "cd " + shellQuote([]string{ws.App}) + " && " + cmd
		}
		steps = append(steps, Run{Cmd: cmd})
	}

	start, err := pythonStart(f)
	if err != nil && f.Start == "" {
		return nil, Runtime{}, err
	}
	rt := Runtime{
		From:   runtimeImage,
		Copies: []Copy{{From: "build", Src: []string{"/app/"}, Dst: "/app/"}},
		Env:    []Env{venvPath, {"PYTHONUNBUFFERED", "1"}, {"HOME", "/tmp"}},
		Cmd:    start,
	}
	if !debian {
		rt.Copies = append([]Copy{{From: "build", Src: []string{pythonDir + "/"}, Dst: pythonDir + "/", RootOwned: true, Exclude: pythonRuntimeExcludes}}, rt.Copies...)
		rt.Env = append(rt.Env, Env{"SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt"})
	}
	if ws := f.UvWorkspace; ws != nil {
		rt.Workdir = "/app/" + ws.App
	}
	return []Stage{{Name: "build", From: buildImage, Steps: steps}}, rt, nil
}

// uvicorn can't read PORT and distroless has no shell, so Python adds --port at startup.
const withPort = "import os, sys; os.execvp(sys.argv[1], sys.argv[1:] + ['--port', os.environ['PORT']])"

// gunicorn binds to 0.0.0.0:$PORT on its own when PORT is set.
var pythonFrameworks = []struct {
	name, dep, server string
	cmd               func(module string) Cmd
	module            func(f *Facts) string
}{
	{"Django", "django", "gunicorn", func(m string) Cmd { return Cmd{"gunicorn", m} }, djangoWSGI},
	{"FastAPI", "fastapi", "uvicorn", func(m string) Cmd { return Cmd{"python", "-c", withPort, "uvicorn", m, "--host", "0.0.0.0"} }, appVariable("FastAPI")},
	{"Flask", "flask", "gunicorn", func(m string) Cmd { return Cmd{"gunicorn", m} }, appVariable("Flask")},
}

var pythonEntries = []string{"main.py", "app.py", "wsgi.py"}

func pythonStart(f *Facts) (Cmd, error) {
	for _, fw := range pythonFrameworks {
		if !f.Python.Deps[fw.dep] {
			continue
		}
		module := fw.module(f)
		if module == "" {
			continue
		}
		if !f.Python.Deps[fw.server] {
			return nil, fmt.Errorf("this is a %s app but %s isn't in its dependencies; add %s", fw.name, fw.server, fw.server)
		}
		return fw.cmd(module), nil
	}
	for _, file := range pythonEntries {
		if f.Has(file) {
			return Cmd{"python", file}, nil
		}
	}
	return nil, fmt.Errorf("no start command: add a main.py, or a Procfile with a web: line")
}

func appVariable(constructor string) func(*Facts) string {
	re := regexp.MustCompile(`(?m)^(\w+)\s*=\s*` + constructor + `\(`)
	return func(f *Facts) string {
		for _, file := range pythonEntries {
			src, _ := f.read(file)
			if m := re.FindStringSubmatch(src); m != nil {
				return strings.TrimSuffix(file, ".py") + ":" + m[1]
			}
		}
		return ""
	}
}

var djangoSettingsRe = regexp.MustCompile(`DJANGO_SETTINGS_MODULE["']\s*,\s*["']([\w.]+)\.settings["']`)
var staticRootRe = regexp.MustCompile(`(?m)^STATIC_ROOT\s*=`)

func djangoProject(f *Facts) string {
	src, _ := f.read("manage.py")
	if m := djangoSettingsRe.FindStringSubmatch(src); m != nil {
		return m[1]
	}
	return ""
}

func djangoWSGI(f *Facts) string {
	if p := djangoProject(f); p != "" {
		return p + ".wsgi"
	}
	return ""
}

// collectstatic exits with an error when STATIC_ROOT is unset.
func djangoCollectsStatic(f *Facts) bool {
	p := djangoProject(f)
	if p == "" || !f.Python.Deps["django"] {
		return false
	}
	settings, _ := f.read(filepath.Join(strings.ReplaceAll(p, ".", "/"), "settings.py"))
	return staticRootRe.MatchString(settings)
}

func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
