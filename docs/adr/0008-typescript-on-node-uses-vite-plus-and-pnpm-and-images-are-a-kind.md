# 8. TypeScript on Node uses Vite+ and pnpm, and images are a kind

This decision records how project-standards checks TypeScript projects that run on Node, and how it ships a container image that no project deploys.

## Context

Some projects must be npm projects. A Paseo plugin is an example: `paseo plugin init` makes an npm TypeScript project, and Deno cannot build one. The Deno team joined Cloudflare in October 2026. The Deno runtime gets security fixes until about October 2027, and then official support stops. New services use Go with templ and Datastar, so the TypeScript that stays is mostly npm code, such as plugins.

project-standards has one check for each language and calls only standard commands (ADR 0006). For Node, there are many lint, format and test tools. The standard must not choose the rules of each project. It must give one set of commands that work in all projects.

The paseo repository builds an image that den runs. No project deploys this image to Cloud Run, so the `cloudrun` kind does not apply.

## Decision

**A project with `package.json` and no `deno.json` is TypeScript on Node. CI checks it with Vite+, and the project uses pnpm.**

- **Detection:** from the files, as for the other languages. There is no language field in `mklv.config.json`.
- **Check:** CI runs `vp install`, then `vp run build` if `package.json` has a build script, then `vp check` and `vp test`. Vite+ (MIT) bundles Vitest, Oxlint, Oxfmt and tsgo. Each project sets its own rules in `vite.config.ts`. The lefthook hooks run `vp check --fix` and `vp test`.
- **Package manager:** pnpm. `vp install` reads the package manager from the lockfile, so CI also passes a yarn or npm project.
- **pnpm settings:** conformance generates `pnpm-workspace.yaml` from a base config and `tools.pnpm` in `mklv.config.json` (ADR 0005). The base config:
  - Holds back a new release for 3 days (`minimumReleaseAge: 4320`). This is the same delay that Renovate uses.
  - Fails if a package loses trust evidence, such as provenance, that an older release had (`trustPolicy: no-downgrade`).
  - Lets only direct dependencies come from git or a tarball URL (`blockExoticSubdeps`).
  - Fails on a dependency build script that the project did not approve (`strictDepBuilds`).

  A project puts `packages` and `allowBuilds` under `tools.pnpm`. `pnpm approve-builds` writes to `pnpm-workspace.yaml` directly. Move what it writes into `mklv.config.json`, because the drift check fails on a hand edit.

**Deno projects keep their check.** Do not add new Deno projects. Move the current ones off Deno before about October 2027.

**The `image` kind builds an image and pushes it to GHCR, and deploys nothing.** It uses the same build as `cloudrun`: the project's `Dockerfile`, or the standard Deno image. The push tags `ghcr.io/dmikalova/<name>:<version>` and `:latest`. A host that runs the image pins the version tag. The image has the `org.opencontainers.image.source` label, which links the package to its repository.

There is no registry setting. All images go to GHCR, and Cloud Run reads them through the Artifact Registry remote repository. Add a setting when a project must push to a second registry.

## Consequences

- A new GHCR package is private. Make it public one time in the package settings on GitHub, if hosts must pull it without a token.
- A Node project must have tests, because `vp test` fails when it finds no test files.
- The `vp` version comes from the project's `vite-plus` dependency, so Renovate updates it with the other dependencies.
