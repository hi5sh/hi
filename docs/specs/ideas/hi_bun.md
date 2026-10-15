# Bun in boxes, templates, and teams

Status: Draft

Dependencies: `hi box` (the base image), `hi init` (the `web` template and
its layers), [hi_team.md](../approved/hi_team.md) (the preview, merges,
and approvals), [hi_team_helper.md](../approved/hi_team_helper.md) (the
helper script), and, for the preview check, [hi_web.md](hi_web.md).

## Goal

Make the JavaScript side of hi as fast and as predictable as uv made the
Python side.

hi's boxes, the `web` template, and every hi team that builds a web app run
JavaScript through npm and Node.js. Each preview of a task's branch runs
`npm ci` first, and each `make check` runs four separate tools. Until the
base image moved to Node.js 24 from nodejs.org, the box had Ubuntu's Node.js
18, which the `web` template can't build on.

[Bun](https://bun.com) is one binary that installs packages, runs scripts
and tests, type-checks, bundles, and serves. Anthropic has owned it since
December 2025, and Claude Code ships as a Bun executable, so hi already
depends on it indirectly. In the base image it installs the `web` template
in about a second and runs its Vite build, Vitest tests, Biome, and type
check unchanged (see [Findings](#findings)).

hi's part is:

- **Bun in every box**, pinned and checked like Node.js;
- **templates that use it**, first as the package manager and runner, later
  as the whole stack for small apps;
- **previews that use it** when a project has a `bun.lock`;
- **a dependency review** before a task with new packages is merged.

Not in scope: hi itself stays in Go. Node.js stays in the box, because Bun
is not fully Node-compatible yet and some tools, such as `agent-browser`,
expect `node` and `npm`.

## The box

The base image installs Bun next to Node.js, the same way: one exact
version, downloaded from the release, checked against a pinned SHA-256 per
architecture, into `/usr/local/bin`. Bun names its Linux builds `x64` and
`aarch64`.

```text
RUN ... curl -fsSLo /tmp/bun.zip \
      "https://github.com/oven-sh/bun/releases/download/bun-v1.4.3/bun-linux-$arch.zip" \
 && echo "$sum  /tmp/bun.zip" | sha256sum -c - \
 && unzip -j /tmp/bun.zip -d /usr/local/bin && ln -s bun /usr/local/bin/bunx
```

A new Bun version is a new Containerfile, so a new image tag, as for any
other change to the base image. `hi install` gets Bun as an optional host
tool, so the people who run hi can use it outside boxes too.

## Templates

### `web`, step 1: Bun as the package manager and runner

The code doesn't change. The tooling does:

| Today | With Bun |
|---|---|
| `package-lock.json` | `bun.lock` |
| `layer.json` setup: `npm ci` | `bun install --frozen-lockfile` |
| `npx --no-install tsc` | `bun check` |
| `npx --no-install biome ci .` | `bun x biome ci .` |
| `npx --no-install vitest run` | `bun x vitest run` |
| `npx --no-install vite build` | `bun x vite build` |

`bun x` still runs a tool on Node.js unless it is told `--bun`. That's the
default here: Bun installs and starts things, and Node.js runs Vite and
Vitest, which are tested on it. `--bun` can follow once team projects have
shown it to be safe.

The template also gets a `bunfig.toml` with safe install defaults:

```toml
[install]
frozenLockfile = true
minimumReleaseAge = 259200   # 3 days: a package hijacked today isn't installed today
```

Bun already runs no package's install scripts unless the package is in
`trustedDependencies`. The template ships with that list empty.

### `app`, step 2: Bun as the whole stack

A second template for the apps hi teams are asked for most often, such as a
form, a table, or a small dashboard for a few admins. It uses one runtime
and almost no packages:

- the server is `Bun.serve` with routes, and the front end is HTML imports
  (`bun ./index.html` in development, with hot reload);
- data lives in SQLite through `bun:sqlite`, in a folder the team backs up;
- tests use `bun test`, with happy-dom for the pages;
- `make run` starts the server on `$PORT`, as the preview expects;
- `bun build --compile` makes production one executable.

Fewer packages means fewer to install, update, review, and allow through
the box proxy. The coder still gets React and TypeScript, so its usual
habits carry over.

## Team previews

`teamRunCommand` picks how a project starts. Bun is added between `make run`
and npm:

1. A Makefile with a `run` target: `make run`.
2. A `bun.lock` and a `start` script: `bun install --frozen-lockfile && bun
   run start`.
3. A `package-lock.json`: `npm ci && npm start`, as today.
4. A `package.json` only: `npm install && npm start`, as today.

The lead's instructions say "make run, or bun run start, or npm start". A
preview restarts on every task branch, so install time is most of the wait
between the coder's push and a working link.

## Dependency review

When a task changes `package.json` or `bun.lock`, `hi team merge` runs
`bun pm diff` against dev and gives the reviewer what it found: packages
added and removed, new install scripts, and new imports of sensitive
modules such as `child_process`. A package that would run an install script
needs an owner's approval in the channel before its name goes into
`trustedDependencies`, like any other approval. The lead tells the people in
plain words: "this change adds 3 packages; one wants to run a script when
it's installed".

`bun audit` runs in `make check`, so a known vulnerability fails the check
like a failing test.

## Later

- **A preview check.** Before the lead says a preview is ready, a short
  script opens it with `Bun.WebView`, visits each page in the app's route
  list, and collects console errors and a screenshot of each. On Linux,
  `Bun.WebView` drives an installed Chrome. [hi_web.md](hi_web.md)'s `check`
  command does the same with Lightpanda, so only one of them should be
  built; see the open questions.
- **Tests for the helper.** `helper/helper.js` is plain JavaScript embedded
  with `go:embed`, and the Go tests only parse it. `bun test` with happy-dom
  could test the chat and select mode, and is skipped where Bun isn't
  installed. The file stays as it is, so `go build` never needs Bun.
- **Bundles.** `agent_bundle.go` installs npm packages with
  `npm install -g --prefix /opt/hi/npm`. `bun add -g` is faster but puts
  them elsewhere, so `NODE_PATH` and the bundle images change with it.
- **Single-file production.** `bun build --compile` for servers, and
  `--compile --target=browser` for apps that are one HTML page.

## Releases

1. Bun in the base image; `web` on Bun as package manager and runner;
   previews that use `bun.lock`.
2. The dependency review in `hi team merge`, and `bun audit` in
   `make check`.
3. The `app` template.
4. The preview check, if `hi web` doesn't cover it; tests for the helper.

## Risks

- **A young rewrite.** Bun 1.4 (2026-08-20) moved from Zig to Rust, and
  1.4.2 fixed regressions two weeks later. Every version is pinned in the
  image and the templates, and moves only after `make check` passes on the
  templates in a box.
- **Node compatibility.** Bun's own notes say it isn't fully Node-compatible
  yet. Running tools on Node.js by default (no `--bun`) and keeping npm
  previews as a fallback keeps a team project from depending on the gaps.
- **Two lockfiles.** A project with both `package-lock.json` and `bun.lock`
  drifts. `hi init` writes one, and the check fails if both exist.
- **Unsanitized Markdown.** `Bun.markdown` passes raw HTML and `javascript:`
  links through. Nothing in hi or the templates renders what people typed
  with it.

## Open questions

- Should `app` live in this repository or in the private templates
  repository, with its own roadmap?
- The preview check: `Bun.WebView` with the box's Chrome, or
  `hi web check` with Lightpanda? Lightpanda is lighter; WebView is already
  in the box once Bun is.
- Should the box move tools to `--bun` once a few team projects run cleanly,
  or stay on Node.js for anything Bun didn't write?
- Is three days the right `minimumReleaseAge`, and should a team owner be
  able to approve a newer version for an urgent fix?

## Findings

Measured on 2026-10-15 on the Strix Halo host, with the `web` template in
the base image (`localhost/hi-box`):

| | Base image with Ubuntu's Node.js 18 | Same image, Bun 1.4.3 added |
|---|---|---|
| Install | `npm ci` works, warns `EBADENGINE` | `bun install`: 109 packages, 1.1 s, from `package-lock.json` |
| `vite build` | fails: `node:util` has no `styleText` | 254 ms |
| `vitest run` | not run | 1 passed, 0.48 s |
| `biome ci .` | not run | clean |
| Type check | `tsc` not run | `bun check`: no errors in 4 files, 98 ms |

Moving the base image to Node.js 24.21.0 from nodejs.org fixed the build
without Bun: `npm ci` and `make check` pass in the new image. Bun is about
making the same steps faster and the dependencies smaller, not about making
them work.

## Sources

- [Bun blog: release notes](https://bun.com/blog)
- [Bun 1.4](https://bun.com/blog/bun-v1.4)
- [Bun 1.3.12: `Bun.WebView` and `Bun.cron`](https://bun.com/blog/bun-v1.3.12)
- [bunfig.toml reference](https://bun.com/docs/runtime/bunfig)
- [Anthropic acquires Bun](https://anthropic.com/news/anthropic-acquires-bun-as-claude-code-reaches-usd1b-milestone)
- [Node.js releases](https://nodejs.org/dist/index.json)
