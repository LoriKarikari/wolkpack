package pack

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

var dotnetProvider = provider{
	name: "dotnet",
	detect: func(f *Facts) Role {
		if f.DotNet == nil {
			return 0
		}
		return Primary
	},
	plan:     planDotNet,
	versions: &dotnetVersions,
	version:  dotnetVersion,
}

const dotnetImages = "mcr.microsoft.com/dotnet/"

// Kestrel reads ASPNETCORE_HTTP_PORTS, not PORT, and the runtime image has no shell to copy one
// into the other. A startup hook runs inside the app before Main and does it there.
const portHook = `internal class StartupHook
{
    public static void Initialize()
    {
        var port = System.Environment.GetEnvironmentVariable("PORT");
        if (!string.IsNullOrEmpty(port) && System.Environment.GetEnvironmentVariable("ASPNETCORE_URLS") == null)
        {
            System.Environment.SetEnvironmentVariable("ASPNETCORE_HTTP_PORTS", port);
        }
    }
}
`

const portHookProject = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>%s</TargetFramework>
    <AssemblyName>TerrilPort</AssemblyName>
  </PropertyGroup>
</Project>
`

func dotnetVersion(f *Facts) (string, error) {
	d := f.DotNet
	v, ok := strings.CutPrefix(d.TargetFramework, "net")
	if _, known := dotnetVersions.release(v); !ok || !known {
		return "", fmt.Errorf("%s targets %q; terril builds net%s", d.Project, d.TargetFramework, strings.Join(dotnetVersions.supported(f.Today), ", net"))
	}
	if _, err := dotnetVersions.pick(v, f.Today); err != nil {
		return "", fmt.Errorf("%s: %w", d.Project, err)
	}
	return v, nil
}

func planDotNet(f *Facts, assets []string) ([]Stage, Runtime, error) {
	d := f.DotNet
	version, err := dotnetVersion(f)
	if err != nil {
		return nil, Runtime{}, err
	}
	sdk := dotnetImages + "sdk:" + version
	if d.SDKVersion != "" {
		sdk = dotnetImages + "sdk:" + d.SDKVersion
	}

	manifests := slices.Clone(d.Projects)
	for _, name := range []string{"global.json", "NuGet.config", "nuget.config", "Directory.Build.props", "Directory.Build.targets", "Directory.Packages.props"} {
		if f.Has(name) {
			manifests = append(manifests, name)
		}
	}
	restore := "dotnet restore " + shellQuote([]string{d.Project})
	if f.Has(path.Join(path.Dir(d.Project), "packages.lock.json")) {
		manifests = append(manifests, path.Join(path.Dir(d.Project), "packages.lock.json"))
		restore += " --locked-mode"
	}
	// Packages stay in the layer, not a cache mount: publish --no-restore needs them, and a
	// cache mount can be emptied between the two steps.
	steps := []Step{
		Env{"DOTNET_CLI_TELEMETRY_OPTOUT", "1"},
		Env{"DOTNET_NOLOGO", "1"},
		Copy{Src: manifests, Dst: "./", Parents: true},
		Run{Cmd: restore},
		// A checkout's bin and obj are host build output, and obj would replace this restore's.
		Copy{Src: []string{"."}, Dst: "./", Exclude: append(slices.Clone(sourceExcludes), "**/bin", "**/obj")},
	}
	steps = append(steps, copyAssets(assets)...)
	steps = append(steps, Run{Cmd: "dotnet publish " + shellQuote([]string{d.Project}) + " -c Release --no-restore -o /out -p:UseAppHost=false"})

	hook := Stage{Name: "port-hook", From: dotnetImages + "sdk:" + version, Workdir: "/hook", Steps: []Step{
		Env{"DOTNET_CLI_TELEMETRY_OPTOUT", "1"},
		Env{"DOTNET_NOLOGO", "1"},
		File{Path: "/hook/TerrilPort.csproj", Content: fmt.Sprintf(portHookProject, d.TargetFramework)},
		File{Path: "/hook/StartupHook.cs", Content: portHook},
		Run{Cmd: "dotnet build -c Release -o /out"},
	}}

	kind := "runtime"
	if d.Web {
		kind = "aspnet"
	}
	// Chiseled images have no shell or package manager. The plain one also has no culture data and
	// forces invariant globalization, so it's only for projects that opted into that themselves.
	from := dotnetImages + kind + ":" + version + "-noble-chiseled"
	if !d.Invariant {
		from += "-extra"
	}
	cmd := Cmd{"dotnet", "/app/" + d.Assembly + ".dll"}
	if f.needsDebianRuntime() {
		from = dotnetImages + kind + ":" + version
	}
	return []Stage{hook, {Name: "build", From: sdk, Steps: steps}}, Runtime{
		From: from,
		Copies: []Copy{
			{From: "port-hook", Src: []string{"/out/TerrilPort.dll"}, Dst: "/terril/TerrilPort.dll", RootOwned: true},
			{From: "build", Src: []string{"/out/"}, Dst: "/app/"},
		},
		Env: []Env{{"DOTNET_STARTUP_HOOKS", "/terril/TerrilPort.dll"}, {"HOME", "/tmp"}},
		Cmd: cmd,
	}, nil
}
