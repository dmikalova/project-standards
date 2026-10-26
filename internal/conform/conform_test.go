package conform

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dmikalova/project-standards/internal/config"
	"github.com/dmikalova/project-standards/internal/generate"
	"github.com/dmikalova/project-standards/templates"
)

const readme = "# p\n"

// goMod is a Go project's go.mod that requires project-standards.
const goMod = "module example.com/p\n\ngo 1.27\n\nrequire " + standardsModule + " v0.1.0\n"

// vulns is govulncheck output with a module finding repeated at the package
// level, two fixes for one module, a vulnerability without a fix and one in
// the standard library.
const vulns = `{"config": {"scanner_name": "govulncheck"}}
{"progress": {"message": "Checking the code against the vulnerabilities..."}}
{"osv": {"id": "GO-1"}}
{"finding": {"osv": "GO-1", "fixed_version": "v0.3.7", "trace": [{"module": "golang.org/x/text", "version": "v0.3.5"}]}}
{"finding": {"osv": "GO-2", "fixed_version": "v0.39.0", "trace": [{"module": "golang.org/x/text", "version": "v0.3.5"}]}}
{"finding": {"osv": "GO-1", "fixed_version": "v0.3.7", "trace": [{"module": "golang.org/x/text", "version": "v0.3.5", "package": "golang.org/x/text/language"}]}}
{"finding": {"osv": "GO-3", "fixed_version": "v1.1.0", "trace": [{"module": "example.com/a", "version": "v1.0.0"}]}}
{"finding": {"osv": "GO-4", "trace": [{"module": "example.com/nofix", "version": "v1.0.0"}]}}
{"finding": {"osv": "GO-4", "trace": [{"module": "example.com/nofix", "version": "v1.0.0", "package": "example.com/nofix"}]}}
{"finding": {"osv": "GO-5", "fixed_version": "v1.27.2", "trace": [{"module": "stdlib", "version": "v1.27.1"}]}}
{"finding": {"osv": "GO-6", "trace": []}}
`

// TestNonGoProject conforms a fresh project without a go.mod: every standard
// file is written, the Go-only ones are not, and a second run changes nothing.
func TestNonGoProject(t *testing.T) {
	dir := newProject(t, map[string]string{"README.md": readme}, 2021, 2023, 2024, 2025)
	f := &fakeGo{}
	r := mustConform(t, dir, f)
	want := []string{
		".commitlint.yaml", ".dockerignore", ".github/workflows/cicd.yaml", ".gitignore",
		".gitleaks.toml", ".markdownlint-cli2.yaml", "LICENSE", "lefthook.jsonc", "paseo.json",
	}
	if !reflect.DeepEqual(r.Changed, want) {
		t.Errorf("changed %v, want %v", r.Changed, want)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings %v, want none", r.Findings)
	}
	if calls := f.goCalls(); calls != nil {
		t.Errorf("ran %v in a project without go.mod", calls)
	}
	for path, content := range map[string][]byte{
		"lefthook.jsonc":              templates.Lefthook,
		"paseo.json":                  templates.Paseo,
		".github/workflows/cicd.yaml": templates.CICD,
		".gitignore":                  templates.Gitignore,
		".dockerignore":               templates.Dockerignore,
	} {
		if got := readFile(t, dir, path); got != string(content) {
			t.Errorf("%s = %q, want the template", path, got)
		}
	}
	license := strings.Replace(templates.License, "{{.Years}} {{.Owner}}",
		"2021, 2023-2025 David Mikalova", 1)
	if got := readFile(t, dir, "LICENSE"); got != license {
		t.Errorf("LICENSE differs from the template with 2021, 2023-2025 David Mikalova:\n%s", got)
	}

	if again := mustConform(t, dir, f); len(again.Changed) != 0 || len(again.Findings) != 0 {
		t.Errorf("second run changed %v, found %v; want nothing", again.Changed, again.Findings)
	}
}

// TestDrift rewrites standard files that differ from their templates.
func TestDrift(t *testing.T) {
	dir := newProject(t, map[string]string{"README.md": readme}, 2026)
	mustConform(t, dir, &fakeGo{})
	writeFiles(t, dir, map[string]string{
		// vex's lefthook.jsonc carries an extra comment.
		"lefthook.jsonc":              "{\n  // Extend the shared base config.\n  \"remotes\": []\n}\n",
		".github/workflows/cicd.yaml": "name: old\n",
		"LICENSE":                     "MIT\n",
		".gitignore":                  "web/app.wasm\n",
		".commitlint.yaml":            "# " + generate.Marker + ". DO NOT EDIT.\nstale: true\n",
	})
	r := mustConform(t, dir, &fakeGo{})
	want := []string{
		".commitlint.yaml",
		".github/workflows/cicd.yaml",
		".gitignore",
		"LICENSE",
		"lefthook.jsonc",
	}
	if !reflect.DeepEqual(r.Changed, want) {
		t.Errorf("changed %v, want %v", r.Changed, want)
	}
	if got := readFile(t, dir, "lefthook.jsonc"); got != string(templates.Lefthook) {
		t.Errorf("lefthook.jsonc = %q, want the template", got)
	}
}

// TestGoProject conforms a Go project without magefiles: it gets the mage stub
// and the Go-only generated configs, project-standards is bumped, and each
// module govulncheck reports is bumped to its highest fix.
func TestGoProject(t *testing.T) {
	dir := newProject(t, map[string]string{"README.md": readme, "go.mod": goMod}, 2026)
	f := &fakeGo{vulns: vulns}
	r := mustConform(t, dir, f)
	want := []string{
		".commitlint.yaml", ".dockerignore", ".github/workflows/cicd.yaml", ".gitignore",
		".gitleaks.toml", ".golangci.yaml", ".markdownlint-cli2.yaml", ".ruleguard.go",
		"LICENSE", "go.mod", "go.sum", "lefthook.jsonc", "magefiles/magefile.go",
		"paseo.json",
	}
	if !reflect.DeepEqual(r.Changed, want) {
		t.Errorf("changed %v, want %v", r.Changed, want)
	}
	wantCalls := []string{
		"go get " + standardsModule + "@latest",
		"go mod tidy",
		"go run govulncheck@v1 -format json ./...",
		"go get example.com/a@v1.1.0 golang.org/x/text@v0.39.0",
		"go mod tidy",
	}
	if calls := f.goCalls(); !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("go calls %q, want %q", calls, wantCalls)
	}
	if got := readFile(t, dir, "magefiles/magefile.go"); got != string(templates.Magefile) {
		t.Errorf("magefile = %q, want the stub", got)
	}
	for _, want := range []string{standardsModule + " v1.9.9", "golang.org/x/text v0.39.0"} {
		if !strings.Contains(readFile(t, dir, "go.mod"), want) {
			t.Errorf("go.mod does not require %s", want)
		}
	}
	wantFindings := []Finding{
		{
			ruleVuln,
			"go.mod",
			"example.com/nofix@v1.0.0 is affected by GO-4, which has no fixed version",
		},
		{
			ruleVuln,
			"go.mod",
			"Go v1.27.1 is affected by GO-5; fixed in Go v1.27.2, so raise the go " +
				"or toolchain directive",
		},
	}
	if !reflect.DeepEqual(r.Findings, wantFindings) {
		t.Errorf("findings %q, want %q", r.Findings, wantFindings)
	}

	again := mustConform(t, dir, f)
	if len(again.Changed) != 0 {
		t.Errorf("second run changed %v, want nothing", again.Changed)
	}
}

// TestGoProjectWithoutStandards does not bump project-standards when go.mod
// does not require it, and bumps nothing without vulnerabilities.
func TestGoProjectWithoutStandards(t *testing.T) {
	dir := newProject(t, map[string]string{
		"README.md": readme,
		"go.mod":    "module example.com/p\n\ngo 1.27\n",
	}, 2026)
	f := &fakeGo{}
	mustConform(t, dir, f)
	want := []string{"go mod tidy", "go run govulncheck@v1 -format json ./..."}
	if calls := f.goCalls(); !reflect.DeepEqual(calls, want) {
		t.Errorf("go calls %q, want %q", calls, want)
	}
}

func TestMagefiles(t *testing.T) {
	const importsCI = "//go:build mage\n\npackage main\n\nimport (\n\t// mage:import ci\n\t\"" +
		ciPackage + "\"\n)\n\nfunc init() { _ = ci.Check }\n"
	const ownTargets = "//go:build mage\n\npackage main\n\nimport \"fmt\"\n\n" +
		"func Build() { fmt.Println() }\n"
	tests := []struct {
		name     string
		files    map[string]string
		findings []string
	}{
		{"magefiles importing ci", map[string]string{
			"magefiles/a.go": ownTargets, "magefiles/b.go": importsCI,
		}, nil},
		{"a root magefile importing ci", map[string]string{"magefile.go": importsCI}, nil},
		{"magefiles with their own targets", map[string]string{
			"magefiles/build.go": ownTargets,
		}, []string{"magefile magefiles"}},
		{"a root magefile with its own targets", map[string]string{
			"magefile.go": ownTargets,
		}, []string{"magefile magefile.go"}},
		{
			"ci imported without mage:import",
			map[string]string{
				"magefiles/a.go": "package main\n\nimport (\n\t// the targets\n\t_ \"" + ciPackage + "\"\n)\n",
			},
			[]string{"magefile magefiles"},
		},
		{"a magefile that does not parse", map[string]string{
			"magefiles/a.go": "package main\n\nimport (\n",
		}, []string{"magefile magefiles"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"README.md": readme, "go.mod": goMod}
			maps.Copy(files, tt.files)
			dir := newProject(t, files, 2026)
			r := mustConform(t, dir, &fakeGo{})
			if slices.Contains(r.Changed, magefileStubPath) {
				t.Error("wrote the stub into a project with magefiles")
			}
			if got := rules(r); !reflect.DeepEqual(got, tt.findings) {
				t.Errorf("findings %v, want %v", got, tt.findings)
			}
			for name, content := range tt.files {
				if got := readFile(t, dir, name); got != content {
					t.Errorf("%s was changed", name)
				}
			}
		})
	}
}

// TestHandWrittenConfig reports a config at a generated path without the
// generated header, and leaves it as it is.
func TestHandWrittenConfig(t *testing.T) {
	const handWritten = "version: \"2\"\nlinters:\n  default: standard\n"
	dir := newProject(t, map[string]string{
		"README.md":      readme,
		"go.mod":         goMod,
		".golangci.yaml": handWritten,
	}, 2026)
	r := mustConform(t, dir, &fakeGo{})
	if slices.Contains(r.Changed, ".golangci.yaml") {
		t.Error("overwrote the hand-written .golangci.yaml")
	}
	want := []Finding{{ruleGenerated, ".golangci.yaml",
		"hand-written config; move its overrides into mklv.config.json, then delete it"}}
	if !reflect.DeepEqual(r.Findings, want) {
		t.Errorf("findings %q, want %q", r.Findings, want)
	}
	if got := readFile(t, dir, ".golangci.yaml"); got != handWritten {
		t.Errorf(".golangci.yaml = %q, want it untouched", got)
	}
}

// TestIgnoreAdditions appends a project's ignore entries in a marked section.
func TestIgnoreAdditions(t *testing.T) {
	dir := newProject(t, map[string]string{
		"README.md":     readme,
		config.FileName: `{"ignore": {"git": ["web/app.wasm", "/bin/"], "docker": ["docs"]}}`,
	}, 2026)
	mustConform(t, dir, &fakeGo{})
	want := string(templates.Gitignore) + "\n# This project's additions, from ignore.git in " +
		"mklv.config.json. Edit them there.\nweb/app.wasm\n/bin/\n"
	if got := readFile(t, dir, ".gitignore"); got != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
	want = string(templates.Dockerignore) + "\n# This project's additions, from ignore.docker " +
		"in mklv.config.json. Edit them there.\ndocs\n"
	if got := readFile(t, dir, ".dockerignore"); got != want {
		t.Errorf(".dockerignore = %q, want %q", got, want)
	}
}

// TestInvalidConfig reports an invalid mklv.config.json and writes nothing
// that depends on it, while the other standard files are still written.
func TestInvalidConfig(t *testing.T) {
	tests := []struct {
		name, config, message string
	}{
		{"schema", `{"kind": "app", "runtime": {"port": 0}, "extra": 1}`,
			"invalid against schema/mklv.config.schema.json: /: additional properties 'extra' " +
				"not allowed; /kind: value must be one of 'cli', 'cloudrun', 'image', 'library', 'infra'; " +
				"/runtime/port: minimum: got 0, want 1"},
		{"JSON", `{"kind": `, "invalid against schema/mklv.config.schema.json: not valid JSON"},
		{"Go types", `{"runtime": {"port": 8080.0}}`,
			"invalid against schema/mklv.config.schema.json: json: cannot unmarshal number 8080.0"},
		{"override", `{"tools": {"commitlint": {"rules": {"a": 1}}}}`,
			"tools.commitlint: rules: cannot merge an object onto a list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newProject(t, map[string]string{"README.md": readme, config.FileName: tt.config},
				2026)
			r := mustConform(t, dir, &fakeGo{})
			if len(r.Findings) != 1 || r.Findings[0].Rule != ruleConfig ||
				r.Findings[0].Path != config.FileName ||
				!strings.Contains(r.Findings[0].Message, tt.message) {
				t.Errorf("findings %q, want one mklv-config finding containing %q", r.Findings,
					tt.message)
			}
			if !slices.Contains(r.Changed, "LICENSE") {
				t.Error("LICENSE was not written")
			}
			if slices.Contains(r.Changed, ".commitlint.yaml") {
				t.Error("a generated config was written from an invalid config")
			}
		})
	}
}

func TestMissingReadme(t *testing.T) {
	dir := newProject(t, nil, 2026)
	r := mustConform(t, dir, &fakeGo{})
	want := []Finding{{ruleReadme, "README.md", "missing; every project needs a README"}}
	if !reflect.DeepEqual(r.Findings, want) {
		t.Errorf("findings %q, want %q", r.Findings, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("wrote a README")
	}
}

// TestProjectStandardsItself refuses to overwrite the files project-standards
// defines, such as the reusable workflow, with their callers.
func TestProjectStandardsItself(t *testing.T) {
	dir := newProject(t, map[string]string{"go.mod": "module " + standardsModule + "\n"}, 2026)
	_, err := conformWith(t, dir, &fakeGo{})
	wantErr(t, err, "this is project-standards itself")
}

// TestFailures covers each way a step fails.
func TestFailures(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		dirs  []string
		f     fakeGo
		want  string
	}{
		{name: "not a repository", f: fakeGo{fail: "git rev-parse"}, want: "git rev-parse"},
		{name: "shallow", f: fakeGo{outputs: map[string]string{
			"git rev-parse --is-shallow-repository": "true\n",
		}}, want: "shallow"},
		{name: "git log", f: fakeGo{fail: "git log"}, want: "git log"},
		{name: "no commits", f: fakeGo{outputs: map[string]string{
			"git log --format=%ad --date=format:%Y": "",
		}}, want: "no commits"},
		{name: "unreadable file", dirs: []string{"LICENSE"}, want: "is a directory"},
		{name: "unreadable config", dirs: []string{config.FileName}, want: "is a directory"},
		{name: "unreadable generated config", dirs: []string{".gitleaks.toml"},
			want: "is a directory"},
		{name: "unreadable go.mod", dirs: []string{"go.mod"}, want: "is a directory"},
		{name: "bad go.mod", files: map[string]string{"go.mod": "module\n"}, want: "go.mod"},
		{name: "go.mod without module", files: map[string]string{"go.mod": "go 1.27\n"},
			want: "no module directive"},
		{name: "unreadable go.sum", files: map[string]string{"go.mod": goMod},
			dirs: []string{"go.sum"}, want: "is a directory"},
		{name: "go get", files: map[string]string{"go.mod": goMod},
			f: fakeGo{fail: "go get"}, want: "go get"},
		{name: "go mod tidy", files: map[string]string{"go.mod": goMod},
			f: fakeGo{fail: "go mod tidy"}, want: "go mod tidy"},
		{name: "govulncheck", files: map[string]string{"go.mod": goMod},
			f: fakeGo{fail: "go run"}, want: "go run"},
		{name: "govulncheck output", files: map[string]string{"go.mod": goMod},
			f: fakeGo{vulns: "{"}, want: "reading govulncheck output"},
		{name: "vulnerability bump", files: map[string]string{"go.mod": goMod},
			f: fakeGo{vulns: vulns, fail: "go get", failNth: 2}, want: "go get example.com/a"},
		{name: "tidy after the bump", files: map[string]string{"go.mod": goMod},
			f: fakeGo{vulns: vulns, fail: "go mod tidy", failNth: 2}, want: "go mod tidy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"README.md": readme}
			maps.Copy(files, tt.files)
			dir := newProject(t, files, 2026)
			for _, d := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			_, err := conformWith(t, dir, &tt.f)
			wantErr(t, err, tt.want)
		})
	}
}

// TestGoSumUnreadableAfterTidy fails when go.sum cannot be read back.
func TestGoSumUnreadableAfterTidy(t *testing.T) {
	dir := newProject(t, map[string]string{"README.md": readme, "go.mod": goMod}, 2026)
	f := &fakeGo{after: func(call string) {
		if call == "go run govulncheck@v1 -format json ./..." {
			if err := os.MkdirAll(filepath.Join(dir, "go.sum"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}}
	f.outputs = map[string]string{"go mod tidy": ""}
	_, err := conformWith(t, dir, f)
	wantErr(t, err, "is a directory")
}

// TestUnwritable fails when a standard file or its directory cannot be
// created.
func TestUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	for _, locked := range []string{".github", ".github/workflows"} {
		t.Run(locked, func(t *testing.T) {
			dir := newProject(t, map[string]string{"README.md": readme}, 2026)
			p := filepath.Join(dir, locked)
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
			_, err := conformWith(t, dir, &fakeGo{})
			wantErr(t, err, "permission denied")
		})
	}
}

func TestYearRanges(t *testing.T) {
	tests := []struct {
		years []int
		want  string
	}{
		{[]int{2026}, "2026"},
		{[]int{2026, 2025, 2024, 2023, 2021}, "2021, 2023-2026"},
		{[]int{2021, 2021, 2022}, "2021-2022"},
		{[]int{2019, 2021, 2023}, "2019, 2021, 2023"},
		{[]int{2018, 2019, 2021, 2024, 2025}, "2018-2019, 2021, 2024-2025"},
	}
	for _, tt := range tests {
		if got := yearRanges(tt.years); got != tt.want {
			t.Errorf("yearRanges(%v) = %q, want %q", tt.years, got, tt.want)
		}
	}
}

func TestParseYears(t *testing.T) {
	years, err := parseYears("2026\n2026\n2024\n")
	if err != nil || !reflect.DeepEqual(years, []int{2026, 2026, 2024}) {
		t.Errorf("parseYears = %v, %v", years, err)
	}
	_, err = parseYears("2026\nMon\n")
	wantErr(t, err, `"Mon" is not a year`)
}

func TestValidateBadSchema(t *testing.T) {
	_, err := validate([]byte("{"), []byte("{}"))
	wantErr(t, err, "EOF")
	_, err = validate([]byte(`{"type": 5}`), []byte("{}"))
	wantErr(t, err, "mklv.config.schema.json")
}

// TestConfigSchemaCompiles keeps the embedded schema usable by conform.
func TestConfigSchemaCompiles(t *testing.T) {
	dir := newProject(t, map[string]string{
		"README.md": readme, config.FileName: `{"$schema": "x", "kind": "cli"}`,
	}, 2026)
	f := &fakeGo{}
	f.t = t
	c := &conformer{dir: dir, run: f.run, report: &Report{}}
	p, err := c.config()
	if err != nil || p == nil || p.Kind != "cli" {
		t.Errorf("config() = %v, %v", p, err)
	}

	saved := configSchema
	t.Cleanup(func() { configSchema = saved })
	configSchema = []byte(`{"type": 5}`)
	_, err = c.config()
	wantErr(t, err, "compiling the mklv.config.json schema")
}

func TestExec(t *testing.T) {
	out, err := Exec(t.TempDir(), "go", "env", "GOOS")
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		t.Errorf("Exec(go env GOOS) = %q, %v", out, err)
	}
	_, err = Exec(t.TempDir(), "go", "no-such-command")
	wantErr(t, err, "go no-such-command: exit status 2: go no-such-command: unknown command")
}

// TestDefaultRunner runs conform with Exec, which Options.Run defaults to.
func TestDefaultRunner(t *testing.T) {
	dir := newProject(t, map[string]string{"README.md": readme}, 2026)
	r, err := Run(Options{Dir: dir})
	if err != nil || !slices.Contains(r.Changed, "LICENSE") {
		t.Errorf("Run = %v, %v", r, err)
	}
}

// TestMagefileStubBuilds keeps the stub gofmt-clean Go that imports ci under
// mage:import.
func TestMagefileStubBuilds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "magefile.go")
	if err := os.WriteFile(p, templates.Magefile, 0o644); err != nil {
		t.Fatal(err)
	}
	if !importsCI(p) {
		t.Error("the stub does not import ci under mage:import")
	}
}
