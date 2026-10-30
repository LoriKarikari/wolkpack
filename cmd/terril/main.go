package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/LoriKarikari/terril/pack"
)

const usage = `terril turns an app directory into a container image.

  terril plan  [--provider name] [--env KEY]... DIR
      print the Dockerfile
  terril build [--provider name] [--env KEY[=VALUE]]... [-t tag] DIR
      build the image with docker
  terril version
      print the version

--env makes a variable available to the build steps, but not to the
running app. Without =VALUE it takes the value from the environment.
`

// Release builds set version with -ldflags. Other builds use the version Go records from the
// module or the git commit.
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		fmt.Println("terril", versionString())
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	provider := fs.String("provider", "", "force the primary provider (go, python, node)")
	tag := fs.String("t", "", "image tag (build only; default: directory name)")
	var env envFlags
	fs.Var(&env, "env", "build-time variable, KEY=VALUE or KEY (repeatable)")
	_ = fs.Parse(os.Args[2:]) // ExitOnError exits instead of returning an error.
	if fs.NArg() != 1 || (cmd != "plan" && cmd != "build") {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	dir := fs.Arg(0)

	if err := run(cmd, dir, *provider, *tag, &env); err != nil {
		fmt.Fprintln(os.Stderr, "terril:", err)
		os.Exit(1)
	}
}

func run(cmd, dir, provider, tag string, env *envFlags) error {
	facts, err := pack.Read(dir)
	if err != nil {
		return err
	}
	// Tests set the date to check end-of-life warnings and refusals without waiting for them.
	if today := os.Getenv("TERRIL_TODAY"); today != "" {
		if facts.Today, err = time.Parse(time.DateOnly, today); err != nil {
			return fmt.Errorf("TERRIL_TODAY: %w", err)
		}
	}
	plan, err := pack.New(facts, provider)
	if err != nil {
		return err
	}
	for _, w := range plan.Warnings {
		fmt.Fprintln(os.Stderr, "terril: warning:", w)
	}
	plan.BuildEnv = env.names
	dockerfile := plan.Dockerfile()
	if cmd == "plan" {
		fmt.Print(dockerfile)
		return nil
	}

	if err := env.check(); err != nil {
		return err
	}
	if tag == "" {
		abs, _ := filepath.Abs(dir)
		tag = strings.ToLower(filepath.Base(abs))
	}
	fmt.Fprintf(os.Stderr, "terril: building %s as %s\n", plan.Summary(), tag)
	args := []string{"build", "-t", tag, "-f", "-"}
	dockerEnv := os.Environ()
	if len(env.names) > 0 {
		// Provenance attestations record build args, and the digest is derived from secret values.
		args = append(args, "--provenance=false", "--build-arg", pack.BuildEnvArg+"="+env.digest())
		for _, n := range env.names {
			args = append(args, "--secret", "id="+n+",env="+n)
			dockerEnv = append(dockerEnv, n+"="+env.values[n])
		}
	}
	docker := exec.Command("docker", append(args, plan.Context)...)
	docker.Env = dockerEnv
	docker.Stdin = strings.NewReader(dockerfile)
	docker.Stdout, docker.Stderr = os.Stdout, os.Stderr
	return docker.Run()
}
