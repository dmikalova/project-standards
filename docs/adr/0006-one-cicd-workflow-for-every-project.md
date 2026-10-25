# 6. One cicd workflow for every project

This decision records how CI/CD is shared across projects in every language.

## Context

There was one reusable workflow per kind of project: `go-cloudrun.yaml`, `deno-cloudrun.yaml` (a Dagger pipeline) and `terramate-apply-all.yaml`. A project that changed language or deployment target had to switch workflows, and a Go CLI such as diatom had no workflow at all. Every project already calls its workflow from `.github/workflows/cicd.yaml`.

## Decision

**One reusable workflow, `cicd.yaml`, serves every project. It works out which languages the project uses from its files, and takes the project's `kind` from `mklv.config.json`.**

1. **Setup:** installs the detected toolchains and authenticates to GCP through WIF.
2. **Check:** runs each language's own check (`mage ci:check`, `deno task check`, `tofu` and terramate validation), plus the shared checks through the project-standards binary.
3. **Release**, on main: svu computes the next version from Conventional Commits and tags it, and `project-standards changelog` writes the notes: the commit titles since the previous release, grouped by type as semantic-release did.
4. **Deploy**, on main, depending on `kind`:
   - **cli**: goreleaser publishes binaries, from a `.goreleaser.yaml` generated like the tool configs (ADR 0005): goreleaser's own default looks for a main package at the root, where a project's command rarely is
   - **cloudrun**: builds the image and deploys to Cloud Run
   - **image**: builds the image and pushes it to GHCR only (ADR 0008)
   - **infra**: terramate apply
   - **library**: a GitHub release with the notes, and nothing to deploy

   cloudrun, image and infra projects get the same GitHub release.
5. **Notify:** every deploy, whether it passed or failed, is posted to the Discord `#deploys` channel through a webhook, with the release notes and the run's warning and notice annotations, such as deprecated action runtimes and runner label migrations. The webhook URL is stored with SOPS in infrastructure, synced to Secret Manager, and read through WIF.

**Scheduled runs are health checks.** The standard caller also runs weekly. A scheduled run checks the project, catching breakage from tool and dependency updates, and re-applies `infra` projects to correct drift. It releases and deploys nothing else, and notifies only on a failure or an annotation.

**Every job runs on a pinned runner image** (`ubuntu-26.04`), not `ubuntu-latest`, so an image migration is a deliberate change. **The Go tools CI runs with `go run` are cached**, keyed on the Go version and their pins, so each pin is built once.

The per-kind workflows and the Deno Dagger pipeline are replaced. Each project's `cicd.yaml` caller is a fixed file that the conformance bot keeps up to date (ADR 0007).
