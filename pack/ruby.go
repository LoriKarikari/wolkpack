package pack

import "fmt"

var rubyProvider = provider{
	name: "ruby",
	detect: func(f *Facts) Role {
		if f.Ruby == nil {
			return 0
		}
		return Primary
	},
	plan:     planRuby,
	versions: &rubyVersions,
	version:  rubyVersion,
}

const bundlePath = "/usr/local/bundle"

var rubyEnv = []Env{
	{"BUNDLE_DEPLOYMENT", "1"},
	{"BUNDLE_WITHOUT", "development:test"},
	{"BUNDLE_PATH", bundlePath},
	{"RAILS_ENV", "production"},
	{"RACK_ENV", "production"},
}

func rubyVersion(f *Facts) (string, error) {
	return rubyVersions.pick(f.Ruby.Version, f.Today)
}

func planRuby(f *Facts, assets []string) ([]Stage, Runtime, error) {
	r := f.Ruby
	// Without bin/rails, the rails command acts as the app generator, not the app.
	if r.Gems["railties"] && !f.Has("bin/rails") {
		return nil, Runtime{}, fmt.Errorf("this is a Rails app but bin/rails is missing; commit the bin/ folder that rails new created")
	}
	v, err := rubyVersion(f)
	if err != nil {
		return nil, Runtime{}, err
	}
	start, err := rubyStart(f)
	if err != nil && f.Start == "" {
		return nil, Runtime{}, err
	}

	manifests := []string{"Gemfile", "Gemfile.lock"}
	if f.Has(".ruby-version") {
		manifests = append(manifests, ".ruby-version")
	}
	install := "bundle install && rm -rf ~/.bundle " + bundlePath + "/ruby/*/cache " + bundlePath + "/ruby/*/bundler/gems/*/.git"
	if r.Gems["bootsnap"] {
		install += " && bundle exec bootsnap precompile --gemfile"
	}
	var steps []Step
	for _, e := range rubyEnv {
		steps = append(steps, e)
	}
	steps = append(steps,
		Copy{Src: manifests, Dst: "./"},
		Run{Cmd: install},
		copySource(),
	)
	steps = append(steps, copyAssets(assets)...)
	if r.Gems["bootsnap"] {
		steps = append(steps, Run{Cmd: "bundle exec bootsnap precompile app/ lib/"})
	}
	if r.Gems["railties"] && (r.Gems["propshaft"] || r.Gems["sprockets-rails"]) {
		cmd := "SECRET_KEY_BASE_DUMMY=1 bin/rails assets:precompile"
		if len(assets) > 0 {
			// Node already built app/assets/builds, and the build image has no Node.
			cmd = "SKIP_JS_BUILD=1 SKIP_CSS_BUILD=1 " + cmd
		}
		steps = append(steps, Run{Cmd: cmd})
	}

	// The full ruby image has compilers and headers for native gems. Its slim image has the
	// same Ruby build on the same Debian release, so the compiled gems load there.
	return []Stage{{Name: "build", From: "ruby:" + v, Steps: steps}}, Runtime{
		From: "ruby:" + v + "-slim",
		Copies: []Copy{
			{From: "build", Src: []string{bundlePath + "/"}, Dst: bundlePath + "/", RootOwned: true, Exclude: gemExcludes},
			{From: "build", Src: []string{"/app/"}, Dst: "/app/"},
		},
		Env: append(rubyEnv, Env{"HOME", "/tmp"}),
		Cmd: start,
	}, nil
}

// Native gems compile their C sources into extensions/, so sources, build logs, docs and
// JRuby sources never load at runtime. License files stay.
var gemExcludes = []string{
	"ruby/*/gems/**/*.c",
	"ruby/*/gems/**/*.h",
	"ruby/*/gems/**/*.o",
	"ruby/*/gems/**/Makefile",
	"ruby/*/gems/**/*.java",
	"ruby/*/gems/**/README*",
	"ruby/*/gems/**/CHANGELOG*",
	"ruby/*/gems/**/*.rdoc",
	"ruby/*/extensions/**/mkmf.log",
	"ruby/*/extensions/**/gem_make.out",
}

// puma only reads PORT through a config file, so Ruby adds --port at startup.
const pumaWithPort = `exec("bundle", "exec", "puma", "--port", ENV.fetch("PORT"))`

func rubyStart(f *Facts) (Cmd, error) {
	gems := f.Ruby.Gems
	switch {
	case gems["railties"]:
		if !gems["puma"] {
			return nil, fmt.Errorf("this is a Rails app but no server is in its Gemfile; add puma")
		}
		// config/puma.rb from rails new reads PORT, and rails server binds to 0.0.0.0 in production.
		return Cmd{"bin/rails", "server"}, nil
	case f.Has("config.ru"):
		if !gems["puma"] {
			return nil, fmt.Errorf("this is a Rack app but no server is in its Gemfile; add puma")
		}
		return Cmd{"ruby", "-e", pumaWithPort}, nil
	}
	return nil, fmt.Errorf("no start command: add a config.ru, or a Procfile with a web: line")
}
