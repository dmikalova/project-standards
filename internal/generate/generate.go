// Package generate renders the generated configs: each tool's base config
// merged with the project's overrides, written in the tool's own format to the
// path the tool reads (ADR 0005).
package generate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"

	"github.com/dmikalova/project-standards/internal/config"
	"github.com/dmikalova/project-standards/templates"
)

// File is one generated file, at a path relative to the project root.
type File struct {
	Path    string
	Content []byte
}

// RuleguardPath is where the ruleguard ruleset is written. The leading dot
// keeps the go command from building it; the base golangci config points
// gocritic at it.
const RuleguardPath = ".ruleguard.go"

type format int

const (
	yamlFormat format = iota
	tomlFormat
)

// A toolConfig maps a tool's base config to the file it is generated into.
type toolConfig struct {
	tool   string // key under tools in mklv.config.json
	base   string // file under templates/base
	path   string // the path the tool reads its config from
	format format
	goOnly bool // generated only in a Go module

	// overrideOnly generates the file only when the project overrides the
	// tool. An empty lefthook-local file in every project would be noise.
	overrideOnly bool
}

// toolConfigs lists every generated config file. Each path is the file the tool
// discovers on its own, so editors and direct runs see the same config as the
// gate:
//   - commitlint (the conventionalcommit Go port) looks for .commitlint.yaml in
//     the current directory or the git root.
//   - gitleaks reads .gitleaks.toml from the scanned source directory.
//   - golangci-lint looks for .golangci.yaml upward from the current directory.
//   - goldmark-lint reads markdownlint-cli2's .markdownlint-cli2.yaml, walking up
//     from the current directory.
//   - lefthook merges lefthook-local.yaml after the project-standards remote,
//     so it is the only layer that can override a shared hook.
//
// misspell has no config file; see Misspell.
var toolConfigs = []toolConfig{
	{tool: "commitlint", base: "commitlint.yaml", path: ".commitlint.yaml", format: yamlFormat},
	{tool: "gitleaks", base: "gitleaks.toml", path: ".gitleaks.toml", format: tomlFormat},
	{
		tool:   "golangci",
		base:   "golangci.yaml",
		path:   ".golangci.yaml",
		format: yamlFormat,
		goOnly: true,
	},
	{
		tool:         "lefthook",
		base:         "lefthook-local.yaml",
		path:         "lefthook-local.yaml",
		format:       yamlFormat,
		overrideOnly: true,
	},
	{
		tool:   "markdownlint",
		base:   "markdownlint-cli2.yaml",
		path:   ".markdownlint-cli2.yaml",
		format: yamlFormat,
	},
}

// Files returns the generated files for a project, sorted by path. goModule
// says whether the project is a Go module: only a Go module gets the Go-only
// files, the golangci-lint config and the ruleguard ruleset. The output depends
// only on the project, goModule and the embedded templates, and keys are
// sorted, so comparing it with the files on disk detects drift.
func Files(p *config.Project, goModule bool) ([]File, error) {
	files := make([]File, 0, len(toolConfigs)+1)
	for _, tc := range toolConfigs {
		if tc.goOnly && !goModule {
			continue
		}
		if _, ok := p.Tools[tc.tool]; tc.overrideOnly && !ok {
			continue
		}
		f, err := tc.render(p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if goModule {
		files = append(files, ruleguard())
	}
	if goModule && p.Kind == "cli" {
		f, err := goreleaser(p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func (tc toolConfig) render(p *config.Project) (File, error) {
	merged, err := merged(tc.tool, tc.base, p)
	if err != nil {
		return File{}, err
	}
	var buf bytes.Buffer
	buf.WriteString(header(tc.tool))
	switch tc.format {
	case yamlFormat:
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		err = enc.Encode(merged)
	case tomlFormat:
		enc := toml.NewEncoder(&buf)
		enc.Indent = ""
		err = enc.Encode(merged)
	}
	if err != nil {
		return File{}, fmt.Errorf("%s: encoding %s: %w", tc.tool, tc.path, err)
	}
	return File{Path: tc.path, Content: buf.Bytes()}, nil
}

// merged loads a tool's base config and merges the project's override onto it.
func merged(tool, base string, p *config.Project) (any, error) {
	tree, err := loadBase(base)
	if err != nil {
		return nil, err
	}
	m, err := config.Merge(tree, p.Override(tool))
	if err != nil {
		return nil, fmt.Errorf("tools.%s: %w", tool, err)
	}
	return m, nil
}

// loadBase decodes an embedded base config into a normalized tree.
func loadBase(name string) (any, error) {
	return decodeBase(templates.Base, name)
}

func decodeBase(fsys fs.FS, name string) (any, error) {
	data, err := fs.ReadFile(fsys, "base/"+name)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if strings.HasSuffix(name, ".toml") {
		err = toml.Unmarshal(data, &tree)
	} else {
		err = yaml.Unmarshal(data, &tree)
	}
	if err != nil {
		return nil, fmt.Errorf("base config %s: %w", name, err)
	}
	return config.Normalize(tree), nil
}

// Marker opens the header of every file project-standards generates. A file at
// a generated path without it was written by hand.
const Marker = "Code generated by project-standards"

// IsGenerated reports whether content carries a generated file's header: a #
// or // comment line starting with Marker among the file's leading comments.
// Build constraints and blank lines may come before it, as in the ruleguard
// ruleset.
func IsGenerated(content []byte) bool {
	for line := range strings.Lines(string(content)) {
		line = strings.TrimSpace(line)
		var text string
		switch {
		case line == "" || strings.HasPrefix(line, "//go:build"):
			continue
		case strings.HasPrefix(line, "#"):
			text = strings.TrimPrefix(line, "#")
		case strings.HasPrefix(line, "//"):
			text = strings.TrimPrefix(line, "//")
		default:
			return false
		}
		if strings.HasPrefix(strings.TrimSpace(text), Marker) {
			return true
		}
	}
	return false
}

// header is the comment every generated tool config starts with. YAML and TOML
// both use # comments.
func header(tool string) string {
	return "# " + Marker + " from mklv.config.json. DO NOT EDIT.\n" +
		"#\n" +
		"# This is the project-standards base config with tools." + tool + " from\n" +
		"# mklv.config.json merged onto it. Edit mklv.config.json instead, then run\n" +
		"# `mage ci:fix`, or `project-standards fix` without Go, to regenerate it.\n\n"
}

// GoreleaserPath is where a cli project's goreleaser config is generated.
const GoreleaserPath = ".goreleaser.yaml"

var goreleaserTemplate = template.Must(template.New("goreleaser").Parse(templates.Goreleaser))

// goreleaser renders the goreleaser config the cicd workflow releases a cli
// project with (ADR 0006): its default build looks for a main package at the
// root, where a project's command rarely is. The binary is named after the
// project and built from the entrypoint's directory, or cmd/<name>.
func goreleaser(p *config.Project) (File, error) {
	if p.Name == "" {
		return File{}, fmt.Errorf("a cli project needs name in %s: its binary is named after it", config.FileName)
	}
	main := "./cmd/" + p.Name
	if p.Entrypoint != "" {
		main = "."
		if dir := path.Dir(path.Clean(p.Entrypoint)); dir != "." {
			main = "./" + dir
		}
	}
	var buf bytes.Buffer
	err := goreleaserTemplate.Execute(&buf, struct{ Marker, Name, Main string }{Marker, p.Name, main})
	return File{Path: GoreleaserPath, Content: buf.Bytes()}, err
}

// ruleguard returns the ruleset file. It is a standard file rather than a
// merged config, so projects cannot override it; a project wanting more rules
// points gocritic's ruleguard rules setting at an extra file of its own.
func ruleguard() File {
	const buildLine = "//go:build ignore\n\n"
	body := bytes.TrimPrefix(templates.Ruleguard, []byte(buildLine))
	var buf bytes.Buffer
	// The build constraint stays first so it is honoured; the generated marker
	// follows it, still ahead of the package clause as the Go convention requires.
	buf.WriteString(buildLine)
	buf.WriteString("// " + Marker + " from its standard ruleguard\n" +
		"// ruleset. DO NOT EDIT. Run `mage ci:fix` to regenerate this file.\n\n")
	buf.Write(body)
	return File{Path: RuleguardPath, Content: buf.Bytes()}
}

// Misspell holds the misspell settings, which the spell check passes as flags
// because misspell has no config file. They come from the base in
// templates/base/misspell.yaml merged with tools.misspell.
type Misspell struct {
	// Ignore lists corrections to skip, passed as -i.
	Ignore []string `json:"ignore"`
	// Locale is "", "US" or "UK", passed as -locale when set.
	Locale string `json:"locale"`
	// Exclude lists path globs never checked. ** matches any number of
	// directories.
	Exclude []string `json:"exclude"`
}

// MisspellSettings returns the project's merged misspell settings.
func MisspellSettings(p *config.Project) (Misspell, error) {
	m, err := merged("misspell", "misspell.yaml", p)
	if err != nil {
		return Misspell{}, err
	}
	// The merged tree only holds JSON-compatible values, so it always marshals.
	data, _ := json.Marshal(m)
	var s Misspell
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Misspell{}, fmt.Errorf("tools.misspell: %w", err)
	}
	s.Locale = strings.ToUpper(s.Locale)
	if s.Locale != "" && s.Locale != "US" && s.Locale != "UK" {
		return Misspell{}, fmt.Errorf("tools.misspell.locale: %q is not US or UK", s.Locale)
	}
	return s, nil
}
