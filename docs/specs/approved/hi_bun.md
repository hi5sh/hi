# Bun in boxes, templates, and teams

Status: Approved (2026-10-15). Slice 1 is built (v0.61.0), slice 2 (v0.63.0).

Dependencies: `hi box` (the base image), `hi init` (the `web` template and
its layers), `hi install`, [hi_team.md](hi_team.md) (the preview, merges,
and approvals), [hi_team_helper.md](hi_team_helper.md) (the helper
script), and, for the preview check, [hi_web.md](../ideas/hi_web.md).

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

- **Bun in every box and on the workstation**, the latest release;
- **templates that use it**, first as the package manager and runner, later
  as the whole stack for small apps;
- **previews that use it** when a project has a `bun.lock`;
- **a dependency review** before a task with new packages is merged.

Not in scope: hi itself stays in Go. Node.js stays in the box, because Bun
is not fully Node-compatible yet and some tools, such as `agent-browser`,
expect `node` and `npm`.

## The box and the workstation

Bun is not pinned. The base image installs the latest release when it is
built, as it does uv: the zip for the architecture (Bun names its Linux
builds `x64` and `aarch64`) from
`github.com/oven-sh/bun/releases/latest/download/`, checked against that
release's `SHASUMS256.txt`, into `/usr/local/bin` with a `bunx` link.
Node.js stays pinned, to the LTS from nodejs.org.

The image is tagged by its Containerfile's hash, so a box keeps the Bun it
was built with until the Containerfile changes or the image is removed;
`podman rmi` on the `hi-box` image gets the latest at the next box.

`hi install bun` installs the same release zip, checked the same way, into
`~/.local/bin` with hi's other tools, since `hi init web` runs
`bun install` on the workstation. Bun's own installer would put it in
`~/.bun/bin` and edit the shell profile. When a template's setup command
is a tool `hi install` knows and it is missing, `hi init` says to run
`hi install <tool>` instead of failing on a missing program.

## Templates

### `web`, step 1: Bun as the package manager and runner

The code doesn't change. The tooling does:

| Today | With Bun |
|---|---|
| `package-lock.json` | `bun.lock` |
| `layer.json` setup: `npm ci` | `bun install --frozen-lockfile` |
| `npx --no-install tsc` | `bun check` |
| `npx --no-install biome ci .` | `bun run biome ci .` |
| `npx --no-install vitest run` | `bun run vitest run` |
| `npx --no-install vite build` | `bun run vite build` |

`bun run <tool>` runs a tool from `node_modules/.bin` and, like
`npx --no-install`, fails rather than download one that isn't installed.
It still runs a tool on Node.js unless it is told `--bun`. That's the
default here: Bun installs and starts things, and Node.js runs Vite and
Vitest, which are tested on it. `--bun` can follow once team projects have
shown it to be safe.

`make check` fails while a `package-lock.json` is there, and says to run
`bun install`, which turns it into `bun.lock`, and then delete it. That is
also how a `web` project made before Bun moves over: `hi init --update`
brings the new Makefile and upgrade notes for `bunfig.toml`, and the
check says what to do.

The template also gets a `bunfig.toml` with a safe install default:

```toml
[install]
minimumReleaseAge = 259200   # 3 days: a package hijacked today isn't installed today
```

The lockfile is frozen with `--frozen-lockfile` on each command that
installs (setup, `make check`, previews), not in `bunfig.toml`, where it
would also stop the coder's `bun add`.

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
2. A `start` script and a `bun.lock`: `bun install --frozen-lockfile &&
   bun run start`.
3. A `package-lock.json`: `npm ci && npm start`, as today.
4. A `package.json` only: `npm install && npm start`, as today.

The lead's instructions say "make run, or bun run start, or npm start". A
preview restarts on every task branch, so install time is most of the wait
between the coder's push and a working link.

## Dependency review

When a task changes a `package.json`, `bun.lock`, or `package-lock.json`,
the broker compares them with where the task started, from git on the
host, without running anything from the project:

- the direct dependencies added, removed, and updated, from `package.json`;
- every package the lockfile adds, removes, or updates, counting what the
  direct ones need;
- names new in `trustedDependencies`;
- a `package.json` whose dependencies changed while its lockfile didn't,
  so the frozen install in checks and previews would fail.

The reviewer's brief gets this as a Packages section, with what to check:
that the work needs each new package, that it is widely used, maintained,
and licensed for the use, and that the install and `bun audit` (or
`npm audit`) pass in its box.

Bun runs a package's install script only when the package is in
`trustedDependencies` or on Bun's own list of vetted packages (367 in Bun
1.4.3, such as esbuild and sharp); every other package's script is
blocked, even with an empty `trustedDependencies`. So a name new in
`trustedDependencies` is what lets new code run on every machine that
installs the app, and `hi team merge` refuses it without an owner's yes:
`hi team merge t-4 --approval <id>`, a yes of its own, never one that
approved something else. The lead asks in plain words: "t-4 needs sharp,
which runs its own code when it's installed, to resize the photos. May
it?"

After a merge, `hi team merge` prints what changed in the packages, and the
lead tells the people in plain words what each new package is for.

`bun audit --audit-level=high` runs in the `web` template's `make check`,
so a high or critical advisory fails the check like a failing test.

`bun pm diff` compares two versions of one package, not two lockfiles, so
hi reads the lockfiles itself. The lockfile records no install scripts;
only an install shows them (`bun pm untrusted`), which is the reviewer's
to run in its box.

## Later

- **A preview check.** Before the lead says a preview is ready, a short
  script opens it with `Bun.WebView`, visits each page in the app's route
  list, and collects console errors and a screenshot of each. On Linux,
  `Bun.WebView` drives an installed Chrome. [hi_web.md](../ideas/hi_web.md)'s `check`
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

1. Bun in the base image and in `hi install`; `web` on Bun as package
   manager and runner; previews that use `bun.lock` (v0.61.0).
2. The dependency review in `hi team merge`, and `bun audit` in
   `make check` (v0.63.0).
3. The `app` template.
4. The preview check, if `hi web` doesn't cover it; tests for the helper.

## Risks

- **A young rewrite, unpinned.** Bun 1.4 (2026-08-20) moved from Zig to
  Rust, and 1.4.2 fixed regressions two weeks later. hi takes the latest
  release rather than pinning one, so a bad release reaches the next image
  or `hi install bun`. The template's lockfile pins the packages, the tools
  that run the build stay on Node.js, and the templates' CI runs
  `make check` with the latest Bun on every change, which is where a
  regression shows first.
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

### Slice 1

On 2026-10-15, with Bun 1.4.3: in the new base image, `hi init web` and
`make check` pass in 1.2 s; in a `hi box` on the `dev` network, through
its proxy, `bun install` takes under a second. `bun check` reported the
same error as `tsc` for a type error.

### Before

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
