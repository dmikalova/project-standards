// Package templates holds the base configs and standard files that
// project-standards writes into projects, embedded so the ci package and the
// binary carry them without reading this repository from disk.
package templates

import "embed"

// Base holds one base config per tool under base/, in the tool's own format.
// A project's overrides from mklv.config.json are merged onto these (ADR 0005).
//
//go:embed base
var Base embed.FS

// Ruleguard is the ruleguard ruleset gocritic loads through the base
// golangci-lint config. It is written into each project verbatim.
//
//go:embed ruleguard/rules.go
var Ruleguard []byte

// The standard files conformance writes into every project (ADR 0007). Their
// names drop any leading dot so they neither embed as hidden files nor act on
// this repository.
var (
	// License is the Apache-2.0 text GitHub's apache-2.0 license template
	// writes, with its appendix copyright line as a text/template taking
	// .Years and .Owner.
	//
	//go:embed conform/LICENSE.tmpl
	License string

	// Lefthook is the lefthook.jsonc every project uses: a remote include of
	// project-standards' base config at main.
	//
	//go:embed conform/lefthook.jsonc
	Lefthook []byte

	// Paseo is the paseo.json every project uses. Its worktree setup trusts
	// each new Paseo worktree for Claude Code and installs the lefthook hooks.
	// Each step is skipped where its tool is missing, such as outside the
	// den Paseo image.
	//
	//go:embed conform/paseo.json
	Paseo []byte

	// CICD is the .github/workflows/cicd.yaml caller of the one reusable
	// workflow (ADR 0006).
	//
	//go:embed conform/cicd.yaml
	CICD []byte

	// Magefile is the magefiles/magefile.go stub a Go project without magefiles
	// gets: it imports the ci targets and nothing else.
	//
	//go:embed conform/magefile.go.tmpl
	Magefile []byte

	// Gitignore is the universal .gitignore, before a project's additions.
	//
	//go:embed conform/gitignore
	Gitignore []byte

	// Dockerignore is the universal .dockerignore, before a project's
	// additions.
	//
	//go:embed conform/dockerignore
	Dockerignore []byte

	// Goreleaser is the .goreleaser.yaml a Go project of kind cli is
	// generated with, as a text/template taking .Marker, .Name and .Main.
	//
	//go:embed goreleaser.yaml.tmpl
	Goreleaser string
)
