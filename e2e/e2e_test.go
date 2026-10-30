package e2e

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"text/tabwriter"
	"time"
)

var (
	terril string
	apps   = flag.String("apps", "", "comma-separated test apps to run (default: all)")
	keep   = flag.Bool("keep", false, "keep the images after the run")
)

var results = struct {
	sync.Mutex
	apps, cache [][]string
	built       []string
	images      int
	pull, disk  int64
}{}

func TestMain(m *testing.M) {
	flag.Parse()
	bin, err := os.MkdirTemp("", "terril-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	terril = filepath.Join(bin, "terril")
	build := exec.Command("go", "build", "-o", terril, "../cmd/terril")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		os.Exit(1)
	}
	for _, tool := range []string{"docker", "dive"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: %s isn't installed\n", tool)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(bin); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
	}
	for _, err := range []error{
		writeTable("results.tsv", []string{"example", "status", "provider", "disk_mb", "pull_mb", "dive_eff", "dive_waste_mb", "user", "detail"}, results.apps),
		writeTable("cache.tsv", []string{"example", "status", "rebuild_s", "steps_rerun", "deps_rerun"}, results.cache),
	} {
		if err != nil {
			fmt.Fprintln(os.Stderr, "e2e:", err)
			code = 1
		}
	}
	// Images go once every test is done, so no build loses layers it shares with another.
	if !*keep && len(results.built) > 0 {
		if out, err := exec.Command("docker", append([]string{"rmi", "-f"}, results.built...)...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: removing images: %v: %s\n", err, out)
		}
	}
	if results.images > 0 {
		fmt.Printf("total: %d images, pull %s MB, disk %s MB\n", results.images, mb(results.pull), mb(results.disk))
	}
	// Keeps recent layers so reruns stay fast, without the cache growing every run.
	if !*keep {
		if out, err := exec.Command("docker", "builder", "prune", "-f", "--max-used-space", "10GB").CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: trimming the build cache: %v: %s\n", err, out)
		}
	}
	os.Exit(code)
}

type fixture struct {
	name        string
	dir         string
	expect      string
	expectError string
	warning     string
	today       string
	path        string
	absent      []string
	buildEnv    []string
	secrets     []string
	runEnv      []string
}

func fixtures(t *testing.T) []fixture {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var only []string
	if *apps != "" {
		only = strings.Split(*apps, ",")
	}
	var fs []fixture
	for _, e := range entries {
		if !e.IsDir() || len(only) > 0 && !slices.Contains(only, e.Name()) {
			continue
		}
		root := filepath.Join("testdata", e.Name())
		f := fixture{name: e.Name(), dir: root, path: "/"}
		if app, ok := readFile(root, "app"); ok {
			f.dir = filepath.Join(root, app)
		}
		f.expect, _ = readFile(root, "expect")
		f.expectError, _ = readFile(root, "expect-error")
		f.warning, _ = readFile(root, "expect-warning")
		f.today, _ = readFile(root, "today")
		if p, ok := readFile(root, "path"); ok {
			f.path = p
		}
		f.absent = readLines(root, "absent")
		f.runEnv = readLines(root, "run-env")
		for _, line := range readLines(root, "build-env") {
			kv, secret := strings.CutPrefix(line, "secret ")
			f.buildEnv = append(f.buildEnv, kv)
			if secret {
				_, v, _ := strings.Cut(kv, "=")
				f.secrets = append(f.secrets, v)
			}
		}
		fs = append(fs, f)
	}
	if len(only) > 0 && len(fs) != len(only) {
		t.Fatalf("-apps names %d apps, testdata has %d of them", len(only), len(fs))
	}
	return fs
}

// php-laravel and Rails apps have an app/ folder, not an app file.
func readFile(dir, name string) (string, bool) {
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	b, err := os.ReadFile(p)
	return strings.TrimSpace(string(b)), err == nil
}

func readLines(dir, name string) []string {
	s, _ := readFile(dir, name)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestApps(t *testing.T) {
	for _, f := range fixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			r := &appResult{name: f.name}
			defer r.record(t)
			testApp(t, f, r)
		})
	}
}

type appResult struct {
	name, status, provider, disk, pull, eff, waste, user, detail string
	pullBytes, diskBytes                                         int64
}

func (r *appResult) fail(t *testing.T, format string, args ...any) {
	t.Helper()
	r.status, r.detail = "FAIL", fmt.Sprintf(format, args...)
	t.Error(r.detail)
}

func (r *appResult) record(t *testing.T) {
	switch {
	case r.status != "":
	case t.Failed():
		r.status, r.detail = "FAIL", "see the test output"
	default:
		r.status = "PASS"
	}
	row := []string{r.name, r.status, r.provider, r.disk, r.pull, r.eff, r.waste, r.user, r.detail}
	for i, v := range row {
		if v == "" {
			row[i] = "-"
		}
	}
	results.Lock()
	results.apps = append(results.apps, row)
	if r.pullBytes > 0 {
		results.images++
		results.pull += r.pullBytes
		results.disk += r.diskBytes
	}
	results.Unlock()
}

func testApp(t *testing.T, f fixture, r *appResult) {
	image := "terril-e2e-" + f.name
	log := newLog(t, f.name+".log")
	keepUntilEnd(image)

	if f.expectError != "" {
		out, err := f.run(log, "build", "-t", image, f.dir)
		switch {
		case err == nil:
			r.fail(t, "build succeeded but should have failed")
		case !strings.Contains(out, f.expectError):
			r.fail(t, "wrong error, see %s", log.Name())
		default:
			r.detail = "failed as expected"
		}
		return
	}

	plantHostArtifacts(t, f.dir)
	if len(f.buildEnv) > 0 {
		// A build with other values first makes a cached build fail the test.
		var stale []string
		for _, kv := range f.buildEnv {
			k, _, _ := strings.Cut(kv, "=")
			stale = append(stale, "--env", k+"=stale-"+k)
		}
		if _, err := f.run(log, append(append([]string{"build"}, stale...), "-t", image, f.dir)...); err != nil {
			r.fail(t, "build failed, see %s", log.Name())
			return
		}
	}
	args := []string{"build"}
	for _, kv := range f.buildEnv {
		args = append(args, "--env", kv)
	}
	out, err := f.run(log, append(args, "-t", image, f.dir)...)
	if err != nil {
		r.fail(t, "build failed, see %s", log.Name())
		return
	}
	if !strings.Contains(out, f.warning) {
		r.fail(t, "the build didn't warn %q, see %s", f.warning, log.Name())
		return
	}

	rootfs, err := exportImage(t, image, f.secrets)
	if err != nil {
		t.Fatal(err)
	}
	r.disk, r.diskBytes = mb(rootfs.size), rootfs.size
	pull, err := countOutput("docker", "save", image)
	if err != nil {
		t.Fatal(err)
	}
	r.pull, r.pullBytes = mb(pull), pull
	eff, waste, err := dive(image)
	if err != nil {
		t.Fatal(err)
	}
	r.eff, r.waste = fmt.Sprint(eff), fmt.Sprint(waste)
	if eff < 95 {
		r.fail(t, "dive efficiency below 95%%; files are duplicated or deleted across layers")
		return
	}
	if slices.ContainsFunc(rootfs.files, func(p string) bool { return strings.HasSuffix(p, "HOST_MARKER") }) {
		r.fail(t, "host node_modules or __pycache__ copied into the image")
		return
	}
	if shipped := shippedPackages(rootfs.files, f.absent); len(shipped) > 0 {
		r.fail(t, "image ships packages it shouldn't: %s", strings.Join(shipped, " "))
		return
	}
	if leaked := leakedValues(t, image, f, rootfs.secretsFound); len(leaked) > 0 {
		r.fail(t, "build env value in the image: %s", strings.Join(leaked, " "))
		return
	}

	plan, err := f.terril(context.Background(), "plan", f.dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^# terril: (.*)$`).FindSubmatch(plan); m != nil {
		r.provider = string(m[1])
	}
	user, err := exec.Command("docker", "image", "inspect", "-f", "{{.Config.User}}", image).Output()
	if err != nil {
		t.Fatal(err)
	}
	r.user = strings.TrimSpace(string(user))
	if uid, _, _ := strings.Cut(r.user, ":"); uid == "" || uid == "0" || uid == "root" {
		r.fail(t, "image runs as root")
		return
	}

	body, err := serve(t, image, f, log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(strings.Fields(body), " ")
	if len(got) > 60 {
		got = got[:60]
	}
	if !strings.Contains(body, f.expect) {
		r.fail(t, "wanted %q, got %q", f.expect, got)
		return
	}
	r.detail = "got: " + got
}

func plantHostArtifacts(t *testing.T, dir string) {
	root := filepath.Join(dir, "host-artifacts")
	for _, d := range []string{"node_modules", "__pycache__"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "HOST_MARKER"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
}

type rootfs struct {
	size         int64
	files        []string
	secretsFound []string
}

func exportImage(t *testing.T, image string, secrets []string) (rootfs, error) {
	var fs rootfs
	id, err := exec.Command("docker", "create", image).Output()
	if err != nil {
		return fs, err
	}
	cid := strings.TrimSpace(string(id))
	defer docker(t, "rm", cid)
	cmd := exec.Command("docker", "export", cid)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fs, err
	}
	if err := cmd.Start(); err != nil {
		return fs, err
	}
	counter := &countingReader{r: out}
	tr := tar.NewReader(counter)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fs, err
		}
		fs.files = append(fs.files, h.Name)
		if len(secrets) > 0 && h.Typeflag == tar.TypeReg {
			content, err := io.ReadAll(tr)
			if err != nil {
				return fs, err
			}
			for _, s := range secrets {
				if bytes.Contains(content, []byte(s)) && !slices.Contains(fs.secretsFound, s) {
					fs.secretsFound = append(fs.secretsFound, s)
				}
			}
		}
	}
	if _, err := io.Copy(io.Discard, counter); err != nil {
		return fs, err
	}
	fs.size = counter.n
	return fs, cmd.Wait()
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func countOutput(name string, args ...string) (int64, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	n, err := io.Copy(io.Discard, out)
	if err != nil {
		return n, err
	}
	return n, cmd.Wait()
}

func mb(bytes int64) string { return fmt.Sprintf("%.1f", float64(bytes)/1e6) }

func dive(image string) (float64, float64, error) {
	out, err := os.CreateTemp("", "dive-*.json")
	if err != nil {
		return 0, 0, err
	}
	if err := out.Close(); err != nil {
		return 0, 0, err
	}
	defer os.Remove(out.Name()) //nolint:errcheck // a leftover temp file doesn't affect the result
	if b, err := exec.Command("dive", "--ci", "--json", out.Name(), image).CombinedOutput(); err != nil {
		return 0, 0, fmt.Errorf("dive: %w: %s", err, b)
	}
	b, err := os.ReadFile(out.Name())
	if err != nil {
		return 0, 0, err
	}
	var report struct {
		Image struct {
			EfficiencyScore  float64 `json:"efficiencyScore"`
			InefficientBytes float64 `json:"inefficientBytes"`
		} `json:"image"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return 0, 0, err
	}
	return math.Floor(report.Image.EfficiencyScore*10000) / 100, math.Floor(report.Image.InefficientBytes/1e5) / 10, nil
}

func shippedPackages(files, absent []string) []string {
	var shipped []string
	for _, pkg := range absent {
		re := regexp.MustCompile(`node_modules/(\.pnpm/|\.bun/)?` + regexp.QuoteMeta(pkg) + `[@/]|\.yarn/cache/` + regexp.QuoteMeta(pkg) + `-npm-|site-packages/` + regexp.QuoteMeta(pkg) + `/`)
		if slices.ContainsFunc(files, re.MatchString) {
			shipped = append(shipped, pkg)
		}
	}
	return shipped
}

func leakedValues(t *testing.T, image string, f fixture, secretsFound []string) []string {
	history, err := exec.Command("docker", "history", "--no-trunc", image).Output()
	if err != nil {
		t.Fatal(err)
	}
	inspect, err := exec.Command("docker", "image", "inspect", image).Output()
	if err != nil {
		t.Fatal(err)
	}
	meta := string(history) + string(inspect)
	var leaked []string
	for _, kv := range f.buildEnv {
		if _, v, _ := strings.Cut(kv, "="); strings.Contains(meta, v) {
			leaked = append(leaked, fmt.Sprintf("%q", v))
		}
	}
	for _, s := range secretsFound {
		leaked = append(leaked, fmt.Sprintf("%q", s))
	}
	return leaked
}

func serve(t *testing.T, image string, f fixture, log *os.File) (string, error) {
	args := []string{"run", "-d"}
	for _, kv := range f.runEnv {
		args = append(args, "-e", kv)
	}
	id, err := exec.Command("docker", append(args, "-p", "127.0.0.1::8080", image)...).Output()
	if err != nil {
		return "", err
	}
	cid := strings.TrimSpace(string(id))
	defer func() {
		logs, _ := exec.Command("docker", "logs", cid).CombinedOutput()
		if _, err := log.Write(logs); err != nil {
			t.Error(err)
		}
		docker(t, "rm", "-f", cid)
	}()
	port, err := exec.Command("docker", "port", cid, "8080").Output()
	if err != nil {
		return "", err
	}
	addr := strings.SplitN(strings.TrimSpace(string(port)), "\n", 2)[0]
	client := &http.Client{Timeout: 2 * time.Second}
	var body string
	for range 30 {
		if res, err := client.Get("http://" + addr + f.path); err == nil {
			b, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode < 400 {
				body = string(b)
				if strings.Contains(body, f.expect) {
					break
				}
			}
		}
		time.Sleep(time.Second)
	}
	return body, nil
}

func TestCache(t *testing.T) {
	for _, f := range fixtures(t) {
		if f.expectError != "" || len(f.buildEnv) > 0 {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			testCache(t, f)
		})
	}
}

var (
	installRe = regexp.MustCompile(`install|uv sync|npm ci|go mod download|dotnet restore|cargo chef cook`)
	// Tool setup, and production prunes that run after the build, aren't the dependency install.
	notInstallRe = regexp.MustCompile(`apt-get|install -g|python install|tool install|prune|--prod|--production|--omit=dev`)
	mountsRe     = regexp.MustCompile(`^(--mount=\S+ )*`)
	runStepRe    = regexp.MustCompile(`^#(\d+) \[[^\]]+\] RUN (.*)$`)
	cachedRe     = regexp.MustCompile(`^#(\d+) CACHED`)
)

func testCache(t *testing.T, f fixture) {
	row := []string{f.name, "FAIL", "-", "-", "first build failed"}
	defer func() {
		results.Lock()
		results.cache = append(results.cache, row)
		results.Unlock()
	}()
	image := "terril-cache-" + f.name
	keepUntilEnd(image)
	work := t.TempDir()
	if err := os.CopyFS(work, os.DirFS(filepath.Join("testdata", f.name))); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, strings.TrimPrefix(f.dir, filepath.Join("testdata", f.name)))
	log := newLog(t, f.name+".cache.log")
	if _, err := f.run(log, "build", "-t", image, dir); err != nil {
		t.Fatalf("first build failed, see %s", log.Name())
	}
	plan, err := f.terril(context.Background(), "plan", f.dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	first := ""
	for _, line := range strings.Split(string(plan), "\n") {
		cmd, ok := strings.CutPrefix(line, "RUN ")
		if cmd = mountsRe.ReplaceAllString(cmd, ""); ok && installRe.MatchString(cmd) && !notInstallRe.MatchString(cmd) {
			first = cmd
			break
		}
	}

	// A fully cached build never touches the steps before a stage's last one, so a size-limited
	// prune can drop them. If the install reran, a second rebuild shows whether the Dockerfile
	// itself keeps it cached.
	var rerun []string
	var secs float64
	for i := range 2 {
		if err := os.WriteFile(filepath.Join(dir, "terril-cache-probe.txt"), []byte(fmt.Sprint(time.Now().UnixNano(), i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := log.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := log.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		out, err := f.run(log, "build", "-t", image, dir)
		secs = time.Since(start).Seconds()
		if err != nil {
			row[4] = "rebuild failed"
			t.Fatalf("rebuild failed, see %s", log.Name())
		}
		rerun = rerunSteps(out)
		if first == "" || !slices.Contains(rerun, first) {
			break
		}
	}
	row = []string{f.name, "PASS", fmt.Sprintf("%.1f", secs), fmt.Sprint(len(rerun)), "-"}
	if first != "" && slices.Contains(rerun, first) {
		row[1], row[4] = "FAIL", first
		t.Errorf("a source edit reran the dependency install: %s", first)
	}
}

func newLog(t *testing.T, name string) *os.File {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join("logs", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	return log
}

// rerunSteps lists the RUN commands BuildKit ran instead of taking from its cache.
func rerunSteps(out string) []string {
	steps, cached := map[string]string{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if m := runStepRe.FindStringSubmatch(line); m != nil {
			steps[m[1]] = mountsRe.ReplaceAllString(m[2], "")
		} else if m := cachedRe.FindStringSubmatch(line); m != nil {
			cached[m[1]] = true
		}
	}
	var rerun []string
	for id, cmd := range steps {
		if !cached[id] && !slices.Contains(rerun, cmd) {
			rerun = append(rerun, cmd)
		}
	}
	return rerun
}

func keepUntilEnd(image string) {
	results.Lock()
	results.built = append(results.built, image)
	results.Unlock()
}

// docker runs a cleanup command. A failure is logged, not fatal, so the remaining cleanups still run.
func docker(t *testing.T, args ...string) {
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Logf("docker %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func (f fixture) run(log *os.File, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	var out bytes.Buffer
	cmd := f.terril(ctx, args...)
	cmd.Stdout = io.MultiWriter(&out, log)
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	return out.String(), err
}

// A test app's today file fixes the date, so end-of-life tests don't change with the calendar.
func (f fixture) terril(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, terril, args...)
	if f.today != "" {
		cmd.Env = append(os.Environ(), "TERRIL_TODAY="+f.today)
	}
	return cmd
}

func writeTable(name string, header []string, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	slices.SortFunc(rows, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	var b strings.Builder
	for _, row := range append([][]string{header}, rows...) {
		b.WriteString(strings.Join(row, "\t") + "\n")
	}
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := tw.Write([]byte(b.String())); err != nil {
		return err
	}
	return tw.Flush()
}
