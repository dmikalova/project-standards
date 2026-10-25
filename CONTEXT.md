# project-standards

The standards every project follows regardless of language, plus the automation that checks projects against them and keeps them in line.

## Language

**Standard**:
A rule every opted-in project must satisfy, such as a file that must match a template, a check that must pass, or a config that must be generated.
_Avoid_: convention, policy, guideline

**Project**:
A single codebase that follows the standards. Deliberately not tied to any code forge's terminology.
_Avoid_: repo, repository, app

**Kind**:
What a project produces and how it is shipped: cli, cloudrun, image, library or infra.
_Avoid_: type, flavor, target

**Base config**:
The standard configuration for a tool, which a project gets by default.
_Avoid_: default config, preset, shared config

**Override**:
A project's change to a base config, declared in `mklv.config.json`.
_Avoid_: customization, local config, patch

**Generated config**:
A tool's config file at its standard path, produced by merging the base config and overrides. It is never edited by hand.
_Avoid_: rendered config, output config

**Drift**:
Any difference between a project and what the standards say it should contain.
_Avoid_: divergence, deviation, staleness

**Conformance**:
The weekly process that removes drift by writing the standard files into each opted-in project and pushing them.
_Avoid_: compliance, enforcement, audit, sync
