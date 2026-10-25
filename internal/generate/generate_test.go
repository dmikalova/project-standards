package generate

import (
	"bytes"
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dmikalova/project-standards/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// TestGolden renders each testdata case and compares every generated config
// with its golden file. A case is a directory holding an optional
// mklv.config.json; its goldens are named after the generated path without the
// leading dot, plus .golden, so no tool mistakes one for a live config. Run
// `go test ./internal/generate -update` after an intended change.
func TestGolden(t *testing.T) {
	for _, name := range []string{"base", "overrides"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", name)
			p, err := config.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			files, err := Files(p, Langs{Go: true, Node: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range files {
				if f.Path == RuleguardPath {
					continue // TestRuleguard covers it; a golden would copy the template.
				}
				golden := filepath.Join(dir, strings.TrimPrefix(f.Path, ".")+".golden")
				if *update {
					if err := os.WriteFile(golden, f.Content, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run with -update to create it)", err)
				}
				if !bytes.Equal(f.Content, want) {
					t.Errorf(
						"%s differs from %s:\n--- got\n%s\n--- want\n%s",
						f.Path,
						golden,
						f.Content,
						want,
					)
				}
			}
		})
	}
}

func TestFilesAreSortedAndDeterministic(t *testing.T) {
	p, err := config.Load(filepath.Join("testdata", "overrides"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Files(p, Langs{Go: true, Node: true})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range first {
		paths = append(paths, f.Path)
	}
	want := []string{
		".commitlint.yaml",
		".gitleaks.toml",
		".golangci.yaml",
		".goreleaser.yaml",
		".markdownlint-cli2.yaml",
		".ruleguard.go",
		"lefthook-local.yaml",
		"pnpm-workspace.yaml",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
	// Go randomizes map iteration, so repeated runs would expose unsorted keys.
	for range 20 {
		again, err := Files(p, Langs{Go: true, Node: true})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatal("Files is not deterministic")
		}
	}
}

func TestFilesWithoutGoModule(t *testing.T) {
	files, err := Files(&config.Project{}, Langs{})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{".commitlint.yaml", ".gitleaks.toml", ".markdownlint-cli2.yaml"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths without a go.mod = %v, want %v", paths, want)
	}
}

func TestFilesNodeOnly(t *testing.T) {
	has := func(l Langs) bool {
		files, err := Files(&config.Project{}, l)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f.Path == "pnpm-workspace.yaml" {
				return true
			}
		}
		return false
	}
	if !has(Langs{Node: true}) {
		t.Error("a Node project got no pnpm-workspace.yaml")
	}
	if has(Langs{Go: true}) {
		t.Error("a project without Node got a pnpm-workspace.yaml")
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		files []string
		want  Langs
	}{
		{nil, Langs{}},
		{[]string{"go.mod"}, Langs{Go: true}},
		{[]string{"package.json"}, Langs{Node: true}},
		{[]string{"package.json", "deno.json"}, Langs{}},
		{[]string{"package.json", "deno.jsonc", "go.mod"}, Langs{Go: true}},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		for _, name := range tt.files {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := Detect(dir); got != tt.want {
			t.Errorf("Detect(%v) = %+v, want %+v", tt.files, got, tt.want)
		}
	}
}

func TestGoreleaser(t *testing.T) {
	tests := []struct {
		name string
		p    config.Project
		main string
	}{
		{"default command directory", config.Project{Name: "diatom", Kind: "cli"}, "main: ./cmd/diatom\n"},
		{"entrypoint", config.Project{Name: "tool", Kind: "cli", Entrypoint: "cmd/tool/main.go"}, "main: ./cmd/tool\n"},
		{"root entrypoint", config.Project{Name: "tool", Kind: "cli", Entrypoint: "main.go"}, "main: .\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := Files(&tt.p, Langs{Go: true})
			if err != nil {
				t.Fatal(err)
			}
			var got *File
			for i := range files {
				if files[i].Path == GoreleaserPath {
					got = &files[i]
				}
			}
			if got == nil {
				t.Fatal("no .goreleaser.yaml for a cli project")
			}
			content := string(got.Content)
			if !strings.Contains(content, tt.main) || !strings.Contains(content, "binary: "+tt.p.Name+"\n") ||
				!IsGenerated(got.Content) || !strings.Contains(content, "main.version={{ .Version }}") {
				t.Errorf("goreleaser config =\n%s", content)
			}
		})
	}
	if _, err := Files(&config.Project{Kind: "cli"}, Langs{Go: true}); err == nil || !strings.Contains(err.Error(), "needs name") {
		t.Errorf("a cli project without a name: %v", err)
	}
	for _, p := range []config.Project{{Name: "x", Kind: "cloudrun"}} {
		files, _ := Files(&p, Langs{Go: true})
		for _, f := range files {
			if f.Path == GoreleaserPath {
				t.Errorf("kind %s got a goreleaser config", p.Kind)
			}
		}
	}
	if files, _ := Files(&config.Project{Name: "x", Kind: "cli"}, Langs{}); len(files) != 3 {
		t.Errorf("a cli project without a go.mod got %d files", len(files))
	}
}

func TestHeaders(t *testing.T) {
	files, err := Files(&config.Project{}, Langs{Go: true, Node: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !bytes.Contains(f.Content[:200], []byte("Code generated by project-standards")) {
			t.Errorf("%s has no generated header", f.Path)
		}
	}
}

func TestRuleguard(t *testing.T) {
	f := ruleguard()
	if !bytes.HasPrefix(f.Content, []byte("//go:build ignore\n\n// Code generated")) {
		t.Errorf("ruleguard file must open with its build constraint, then the generated marker")
	}
	// TestRuleguardImportsKept in the ci package checks the file's imports.
	if _, err := parser.ParseFile(
		token.NewFileSet(),
		f.Path,
		f.Content,
		parser.ParseComments,
	); err != nil {
		t.Fatalf("ruleguard file does not parse: %v", err)
	}
}

func TestMisspellSettings(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    Misspell
		wantErr string
	}{
		{
			name: "base",
			json: `{}`,
			want: Misspell{Ignore: []string{}, Exclude: []string{"**/go.sum"}},
		},
		{
			name: "overrides",
			json: `{"tools":{"misspell":{"ignore":["cancelled"],"locale":"uk","exclude":["docs/**"]}}}`,
			want: Misspell{
				Ignore:  []string{"cancelled"},
				Locale:  "UK",
				Exclude: []string{"**/go.sum", "docs/**"},
			},
		},
		{
			name:    "bad locale",
			json:    `{"tools":{"misspell":{"locale":"AU"}}}`,
			wantErr: `"AU" is not US or UK`,
		},
		{
			name:    "unknown key",
			json:    `{"tools":{"misspell":{"words":[]}}}`,
			wantErr: `unknown field "words"`,
		},
		{
			name:    "wrong type",
			json:    `{"tools":{"misspell":{"locale":1}}}`,
			wantErr: "tools.misspell:",
		},
		{
			name:    "conflict",
			json:    `{"tools":{"misspell":{"ignore":{"a":1}}}}`,
			wantErr: "tools.misspell: ignore: cannot merge",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := config.Parse([]byte(tt.json))
			if err != nil {
				t.Fatal(err)
			}
			got, err := MisspellSettings(p)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MisspellSettings = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFilesMergeConflict(t *testing.T) {
	p, err := config.Parse([]byte(`{"tools":{"golangci":{"linters":{"enable":{"x":true}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Files(p, Langs{Go: true, Node: true})
	if err == nil || !strings.Contains(err.Error(), "tools.golangci: linters.enable") {
		t.Errorf("err = %v, want a conflict at tools.golangci: linters.enable", err)
	}
}

func TestEncodeError(t *testing.T) {
	// A null list element survives the merge (null only removes keys), but TOML
	// has no null, so encoding the gitleaks config fails with a clear error.
	p, err := config.Parse([]byte(`{"tools":{"gitleaks":{"x":[null]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Files(p, Langs{Go: true, Node: true})
	if err == nil || !strings.Contains(err.Error(), "gitleaks: encoding .gitleaks.toml") {
		t.Errorf("err = %v, want a TOML encoding error", err)
	}
}

func TestMissingBase(t *testing.T) {
	tc := toolConfig{
		tool:   "gitleaks",
		base:   "missing.toml",
		path:   ".gitleaks.toml",
		format: tomlFormat,
	}
	if _, err := tc.render(&config.Project{}); err == nil {
		t.Error("render with a missing base succeeded")
	}
	p := &config.Project{Tools: map[string]any{"misspell": map[string]any{}}}
	if _, err := MisspellSettings(p); err != nil {
		t.Error(err)
	}
}

func TestDecodeBase(t *testing.T) {
	fsys := fstest.MapFS{
		"base/bad.yaml": {Data: []byte("a: [")},
		"base/bad.toml": {Data: []byte("a = ")},
		"base/ok.toml":  {Data: []byte("[[a]]\nb = 1\n")},
	}
	for _, name := range []string{"bad.yaml", "bad.toml"} {
		if _, err := decodeBase(fsys, name); err == nil {
			t.Errorf("decodeBase(%s) succeeded", name)
		}
	}
	got, err := decodeBase(fsys, "ok.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": []any{map[string]any{"b": int64(1)}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodeBase(ok.toml) = %#v, want %#v", got, want)
	}
}

func TestIsGenerated(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"# Code generated by project-standards from mklv.config.json. DO NOT EDIT.\nx: 1\n", true},
		{"//go:build ignore\n\n// Code generated by project-standards from its standard\n", true},
		{"# A hand comment\n#Code generated by project-standards\n", true},
		{"version: \"2\"\n# Code generated by project-standards\n", false},
		{"# Code generated by something else. DO NOT EDIT.\n", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsGenerated([]byte(tt.content)); got != tt.want {
			t.Errorf("IsGenerated(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

// TestFilesAreGenerated keeps every generated file detectable as generated, so
// conformance regenerates it rather than reporting it as hand-written.
func TestFilesAreGenerated(t *testing.T) {
	files, err := Files(&config.Project{}, Langs{Go: true, Node: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !IsGenerated(f.Content) {
			t.Errorf("%s does not carry the generated header", f.Path)
		}
	}
}
