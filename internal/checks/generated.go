package checks

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/dmikalova/project-standards/internal/config"
	"github.com/dmikalova/project-standards/internal/generate"
)

// Drift fails if a generated config differs from what WriteGenerated writes.
// Generated configs are the base configs merged with mklv.config.json; a hand
// edit to one is drift, and the fix is to move the change into
// mklv.config.json. Configs for a language the project does not use are not
// checked.
func (c *Checks) Drift() error {
	files, err := generatedFiles()
	if err != nil {
		return err
	}
	var drifted []string
	for _, f := range files {
		have, err := os.ReadFile(f.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			drifted = append(drifted, f.Path+" (missing)")
		case err != nil:
			return err
		case !bytes.Equal(have, f.Content):
			drifted = append(drifted, f.Path+" (differs)")
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("generated configs are out of date (run %s, and move any "+
			"hand edits into %s):\n  %s", c.FixCommand, config.FileName,
			strings.Join(drifted, "\n  "))
	}
	return nil
}

// WriteGenerated writes every generated config that is missing or differs,
// leaving up-to-date files untouched. Configs for a language the project does
// not use are not written.
func (c *Checks) WriteGenerated() error {
	files, err := generatedFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		if have, err := os.ReadFile(f.Path); err == nil && bytes.Equal(have, f.Content) {
			continue
		}
		fmt.Printf("%s: writing %s\n", c.label("fix"), f.Path)
		if err := os.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// generatedFiles returns the generated configs that apply to the project in
// the current directory.
func generatedFiles() ([]generate.File, error) {
	p, err := config.Load(".")
	if err != nil {
		return nil, err
	}
	return generate.Files(p, generate.Detect("."))
}
