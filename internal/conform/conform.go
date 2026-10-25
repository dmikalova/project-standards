// Package conform brings one project in line with the standards (ADR 0007).
// It writes every standard file that has drifted and reports what needs a
// human. The weekly conformance workflow runs it, through `project-standards
// conform`, in each opted-in project's checkout, then runs the project's own
// check and commits the result.
//
// What it writes is a function of the project's files, its commit history, the
// embedded templates and, for Go dependencies, the module proxy and the
// vulnerability database, so a second run changes nothing.
package conform

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dmikalova/project-standards/internal/config"
	"github.com/dmikalova/project-standards/internal/generate"
	"github.com/dmikalova/project-standards/templates"
)

// Owner is the copyright owner LICENSE names.
const Owner = "David Mikalova"

// standardsModule is project-standards' own module path.
const standardsModule = "github.com/dmikalova/project-standards"

// The standard files' paths, relative to the project root.
const (
	licensePath      = "LICENSE"
	lefthookPath     = "lefthook.jsonc"
	cicdPath         = ".github/workflows/cicd.yaml"
	magefileStubPath = "magefiles/magefile.go"
	gitignorePath    = ".gitignore"
	dockerignorePath = ".dockerignore"
	readmePath       = "README.md"
)

// The rules a Finding can name.
const (
	ruleConfig    = "mklv-config"
	ruleGenerated = "generated-config"
	ruleMagefile  = "magefile"
	ruleVuln      = "govulncheck"
	ruleReadme    = "readme"
)

// Report is what one run changed and what it found for a human.
type Report struct {
	// Changed lists the files written, relative to the project root, sorted.
	Changed []string `json:"changed"`
	// Findings lists what conform reports but never changes, in rule order.
	Findings []Finding `json:"findings"`
}

// Finding is one thing a human has to resolve.
type Finding struct {
	// Rule names the standard, such as generated-config or readme.
	Rule string `json:"rule"`
	// Path is the file concerned, relative to the project root.
	Path string `json:"path"`
	// Message says what is wrong and what to do.
	Message string `json:"message"`
}

// Runner runs a command in dir and returns its standard output. Conform runs
// git and go through it, so tests can replace the network-dependent go
// commands.
type Runner func(dir, name string, args ...string) ([]byte, error)

// Exec is the Runner that runs the command. A failure's error carries the
// command's stderr.
func Exec(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Options configures a run.
type Options struct {
	// Dir is the project root: a git checkout with its full history.
	Dir string
	// Govulncheck pins govulncheck as a `go run` package@version.
	Govulncheck string
	// Run runs git and go. Nil means Exec.
	Run Runner
}

// Run conforms the project in o.Dir and reports what it changed and found.
// Findings are not errors; it fails only when a step itself fails.
func Run(o Options) (*Report, error) {
	c := &conformer{
		dir:         o.Dir,
		run:         o.Run,
		govulncheck: o.Govulncheck,
		report:      &Report{Changed: []string{}, Findings: []Finding{}},
	}
	if c.run == nil {
		c.run = Exec
	}
	mod, err := c.goMod()
	if err != nil {
		return nil, err
	}
	if mod != nil && mod.Module.Mod.Path == standardsModule {
		return nil, errors.New("this is project-standards itself, which defines the " +
			"standard files; conform runs in the projects that follow them")
	}
	files, err := c.standardFiles(mod != nil)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := c.write(f.Path, f.Content); err != nil {
			return nil, err
		}
	}
	if mod != nil {
		if err := c.goDeps(mod); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(c.path(readmePath)); errors.Is(err, fs.ErrNotExist) {
		c.find(ruleReadme, readmePath, "missing; every project needs a README")
	}
	slices.Sort(c.report.Changed)
	return c.report, nil
}

// conformer holds one run's state.
type conformer struct {
	dir         string
	run         Runner
	govulncheck string
	report      *Report
}

// standardFiles returns every standard file the project should contain. Rules
// that report instead of writing add their findings as they go.
func (c *conformer) standardFiles(goModule bool) ([]generate.File, error) {
	license, err := c.license()
	if err != nil {
		return nil, err
	}
	files := []generate.File{
		license,
		{Path: lefthookPath, Content: templates.Lefthook},
		{Path: cicdPath, Content: templates.CICD},
	}
	if goModule && c.needsMagefile() {
		files = append(files, generate.File{Path: magefileStubPath, Content: templates.Magefile})
	}
	p, err := c.config()
	if err != nil {
		return nil, err
	}
	if p == nil {
		return files, nil
	}
	generated, err := c.generated(p, goModule)
	if err != nil {
		return nil, err
	}
	files = append(files, generated...)
	return append(files, ignoreFiles(p)...), nil
}

// generated returns the generated configs to write. A file at a generated path
// without the generated header was written by hand: it is reported, not
// overwritten, because deleting it could silently drop real exclusions.
func (c *conformer) generated(p *config.Project, goModule bool) ([]generate.File, error) {
	all, err := generate.Files(p, generate.Langs{Go: goModule, Node: generate.Detect(c.dir).Node})
	if err != nil {
		c.find(ruleConfig, config.FileName, err.Error()+"; the generated configs were not written")
		return nil, nil
	}
	var files []generate.File
	for _, f := range all {
		have, ok, err := c.read(f.Path)
		if err != nil {
			return nil, err
		}
		if ok && !generate.IsGenerated(have) {
			c.find(ruleGenerated, f.Path,
				"hand-written config; move its overrides into mklv.config.json, then delete it")
			continue
		}
		files = append(files, f)
	}
	return files, nil
}

// find records a finding.
func (c *conformer) find(rule, path, message string) {
	c.report.Findings = append(c.report.Findings, Finding{Rule: rule, Path: path, Message: message})
}

// path returns a project-relative, slash-separated path on disk.
func (c *conformer) path(rel string) string {
	return filepath.Join(c.dir, filepath.FromSlash(rel))
}

// read returns a project file's contents and whether it exists.
func (c *conformer) read(rel string) ([]byte, bool, error) {
	data, err := os.ReadFile(c.path(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// write writes a project file, creating its directories, unless it already
// holds content, and records it as changed.
func (c *conformer) write(rel string, content []byte) error {
	have, ok, err := c.read(rel)
	if err != nil {
		return err
	}
	if ok && bytes.Equal(have, content) {
		return nil
	}
	p := c.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		return err
	}
	c.report.Changed = append(c.report.Changed, rel)
	return nil
}
