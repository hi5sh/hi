# `hi agent best-of --validate` specification

Status: Approved (2026-10-12). Release 1 built in v0.52.0 (see [As built](#as-built-release-1)).

Dependencies: [hi_agent_best_of.md](hi_agent_best_of.md)
(rounds, snapshots, `--edit`, the gains' branch), `hi box` (the engine and
the project's box image), and
the autoresearch templates (`autoresearch-base` and the templates built on
it).

## Goal

Keep a gain only when it also holds on data the agents never see.

In rounds, every kept change is chosen on the score, and the score reads
data that is in the box. Over tens of rounds and several boxes a round, the
search fits that data: the first long run of the quant template raised its
in-sample score by about three quarters while its holdout score fell. The
holdout catches this, but only at the end, once, and too late to steer the
run. Nothing during the run tells a gain that generalizes from one that
fits.

A validation window does. The data is split three ways: what agents see
(the score), a window after it that only hi sees (validation), and the
holdout, which nobody looks at until the end. A result is kept only if it
beats the best score and doesn't lose on validation.

```sh
hi agent best-of 3 --rounds 20 --score "make score" --higher --edit strategy.py \
  --validate "make validate" --validate-data ../quant-validation program.md
```

## Flags

| Flag | Meaning |
|---|---|
| `--validate "<cmd>"` | hi runs it in a validation container of its own after the round, and takes the last number it prints; same direction as the score |
| `--validate-data <dir>` | a folder on the host, outside the project, mounted read-only at `/box/data` in validation containers only |
| `--validate-slack 0.05` | how far the validation number may fall below the best's and still pass (default 0) |

`--validate` needs `--score` and `--edit`: hi must know which files are the
agent's.

## How it works

Validation never runs in an agent's box. The code being validated is the
agent's own, and in its box the agent could have prepared a way out: the
project's `.git` is writable there (only `hooks` and `config` are
read-only), `--edit` is checked only after the score has run, so a changed
`Makefile` or `evaluate.py` would run with the data in reach, and the box's
proxy still reaches the agent's model. So:

1. **Agents' boxes are unchanged.** They run, check, and score as today.
   `--validate-data` is never mounted in them, and nothing in the wrapper
   changes.
2. **A fresh container per good box.** When the round's boxes are scored
   and measured, hi validates each good box (check passed, a score, inside
   `--edit`, something changed). For each it builds a folder outside the
   project: the gains' tip from `git archive`, with only the `--edit` files
   replaced from that box's snapshot. The harness, the `Makefile`, and the
   data in `eval/` are hi's copies from the tip, whatever the agent did to
   its own. hi runs the project's box image on it directly through the
   engine, not as a hi box: `--network none`, no proxy, no `.git`, no home
   folder from the agent's box, `--validate-data` read-only at
   `/box/data`, the folder at `/box/project`, and the command under a
   time limit of twice the baseline validation's time plus a minute. One
   container start per good box per round; validating every good box, not
   just the winner, lets the report show every attempt's number. For a
   backtest that costs seconds; for a template whose validation trains
   again (`autoresearch-ml`), one more training run per good box. The container and its folder are removed
   when it exits.
3. **The baseline is validated the same way**, on your last commit as it
   is, before the first round. A validation that fails or prints no number
   stops the run before it costs tokens.
4. **The gate.** The round's winner is chosen on the score alone, as
   today. It is kept only if its score beats the best by more than
   `--min-gain` *and* its validation number is no worse than the best's
   validation number by more than `--validate-slack`. If the winner fails
   the gate, the round is `rejected`; hi doesn't fall back to the
   second-best box, since picking among boxes by validation would fit the
   validation window too. A kept result's validation number becomes the
   one later rounds must not lose. Comparing with the best's number, not
   the baseline's, stops validation sliding back to where it started one
   small loss at a time.

With `--gpu`, validation containers take turns like the scores, and get
the same GPU devices as boxes; without it they run at once.

The container has no network, so the image must hold everything the
command needs. Both autoresearch templates bake their dependencies into
`/opt/venv`; a project whose packages are fetched when a box starts fails
at the baseline validation, before any tokens are spent. The
devcontainer's `postCreateCommand` doesn't run there.

### What agents see

Validation numbers and data are never in anything a box can read:

- not in the next round's task: the history table shows each round's score
  and result (`kept`, `discarded`, `rejected`, `crash`), never a
  validation number;
- not in commit messages on the gains' branch or the attempt refs, which
  boxes can read through the project's `.git`;
- not in any agent's box: the data reaches only validation containers,
  which have no network and no `.git`, and whose output goes to hi alone.

They are in the run's state (`run.json`, outside every box), `watch`,
`show`, `ls --json`, and `loop.log`, so a `--then` results command can
report them. `--then` runs in your checkout and the templates write
reports to `results/`, which git ignores. A report with validation numbers
committed to the gains' branch before a `resume` would reach the next
boxes through `.git`; don't commit one there.

`rejected` tells agents one bit: a change that raised the score didn't
hold up. That is the point of the gate, and agents can work it out anyway
from a higher score that wasn't kept. They learn which kinds of ideas fail,
not the numbers.

### The view

```text
Best-of b-9 · "program.md" · 3 boxes a round · round 7 of 20 · 1h12m · $9.40 spent
Score: make score (higher is better) · validated by: make validate
Best: 1.12 (validation 0.81) from round 4, baseline 0.86 (0.79) · on best-of/b-9/best

  ROUND  RESULT     SCORE  VALID.  BOX            IDEA
  4      kept       1.12   0.81    quant-b9-r4-1  inverse-volatility weights
  5      rejected   1.31   0.62    quant-b9-r5-3  six lookbacks fitted per asset
  6      discarded  1.09   –       quant-b9-r6-2  add a crash filter
```

## Expanding window (release 2)

One validation window wears out: every gate it decides is a little
selection on it. Walk-forward folds keep the gate fresh and give agents
more history as the run goes on.

```sh
hi agent best-of 3 --rounds 60 --score "make score" --higher --edit strategy.py \
  --validate "make validate" --validate-data ../quant-validation \
  --folds 3 --fold "make fold" program.md
```

| Flag | Meaning |
|---|---|
| `--folds n` | the run moves through n folds |
| `--fold "<cmd>"` | run on the host, in a worktree of the gains' tip, to move to the next fold; `HI_FOLD` holds its number |
| `--fold-every k` | move on after k rounds; without it, a fold ends when `--patience` runs out |

On a fold change hi:

1. runs `--fold` on the host, in a temporary worktree of the gains' tip
   (your own checkout may be on another branch), with `HI_FOLD` set. The
   tip's harness and `Makefile` are yours: agents' changes outside `--edit`
   are never committed. The command moves the
   window: the old validation block joins the data in the project, and the
   next block becomes `--validate-data`. It must not touch files inside
   `--edit`;
2. commits what it changed to the gains' branch itself, with a message
   that names the fold and nothing else;
3. scores the gains' tip in a box and validates it in a validation
   container again, as a new baseline: the best
   so far is measured on the new windows, not carried over;
4. resets `--patience` and goes on.

The last fold's validation block ends where the holdout starts, so the
holdout is untouched by all of them. A run that ends early keeps whatever
fold it was in; `resume` goes on from there.

## Templates

`autoresearch-base` and the templates on it get a three-way split:

- `prepare.py` writes the data agents see into `eval/` (committed, as
  today), the validation window to `../<folder>-validation/` with the
  history before it, and everything to `../<folder>-holdout/`, as today.
  The holdout never enters a box.
- `evaluate.py --validation <path>` scores only the validation window, the
  way `--holdout` scores only the holdout.
- `make validate` runs it on `/box/data`, and `make loop` passes
  `--validate` and `--validate-data`.
- With folds, `make fold` rewrites `eval/` and the validation folder for
  `HI_FOLD`.
- The results report shows the validation number of every attempt.

Each template says how long its windows are; the private quant template's
plan is in its own repository.

## Releases

1. `--validate`, `--validate-data`, `--validate-slack`: validation
   containers built from the tip and the `--edit` files, without network
   or `.git`; the gate, `rejected`, the view, and the numbers kept out of
   the boxes. Tests: a box whose agent changes the `Makefile`, writes into
   `.git`, or leaves a process running never has the data in reach. `autoresearch-base` and
   `autoresearch-ml` use them.
2. Folds: `--folds`, `--fold`, `--fold-every`, the re-baseline, and
   `make fold` in the templates.

## Risks

- **Validation is fitted too, slowly.** Each gate leaks a bit. A run of 20
  rounds asks about 20 questions of the window, against the hundreds of
  scores agents see in the box. Folds (release 2) spread them out; the
  holdout stays the final word.
- **Less data to search on.** The validation window comes out of what
  agents used to see. Short windows give noisy numbers; folds give the
  data back as the run goes on.
- **A noisy gate rejects real gains.** A change that leaves validation
  unchanged in expectation fails a gate of 0 about half the time.
  `--validate-slack` should be about the window's noise; a template can
  print a suggestion, as the quant template's `make robust` does for
  `--min-gain`.
- **The agent's code still runs with the data.** The `--edit` files run
  in the validation container, so they can read the data and print it.
  The output goes only to hi: the container has no network, no `.git`,
  and nothing of it is kept but the number and the last lines of its
  output, in the run's state. The templates' harness also refuses
  editable code that opens files or imports outside a short list.

## Open questions

1. Whether a rejected round should fall back to the next-best box that
   passes. That keeps more gains but chooses by validation.
2. Whether hi should record every look at the holdout as well, so a run
   reports how often validation was asked and how often the holdout was.
   That is the "record of every holdout look" item for `autoresearch-base`
   on the [roadmap](../../roadmap.md); the two should be built together.

## As built (release 1)

v0.52.0, 2026-10-12. Code: `agent_best_of_validate.go`; tests:
`agent_best_of_validate_test.go`.

- **Flags.** `--validate` needs `--score` and `--edit`;
  `--validate-data` must be a folder outside the project (checked after
  symlinks); `--validate-slack` is 0 or more. `--validate-data` and
  `--validate-slack` need `--validate`.
- **The folder.** `git archive` of the gains' tip, then `git archive` of the
  box's snapshot for its changed files (all inside `--edit`, since only good
  boxes are validated); a changed file missing from the snapshot is
  removed. Written to `best-of/<run>/validate/<box>/project` in hi's state
  and removed after the run of the container.
- **The container.** `run --rm --network none`, the user's uid as in boxes,
  the boxes' hardening (no capabilities, no new privileges, pids and memory
  limits), the GPU devices with `--gpu`, the folder at `/box/project`, an
  empty home at `/box/home`, `--validate-data` at `/box/data:ro`, the
  project's `containerEnv`, `HI_BOX` (so a `Makefile` picks the image's
  tools) and `HI_VALIDATE=1`. The image is the round box's, or the
  baseline box's. Checked live with Podman 5.7: no network, the data
  read-only, no `.git`, the folder removed afterwards.
- **State.** `run.json` has `validate`, `validate_data`, `validate_slack`,
  `validate_max_seconds`, `image`, `baseline_validation`, and
  `best_validation` on the loop; `validation` (value, exit, seconds, the
  last five lines of output) on each box; and the winner's `validation` on
  each round.
- **The task** says that hi also tries the round's best on data that isn't
  in the box and rejects a gain that does worse there, and shows `rejected`
  in the history, never a number.
- **Templates.** `autoresearch-ml` splits 70/10/10/10 (train, val,
  validation, holdout); `make validate` scores on `/box/data` with
  `HI_VALIDATE` set and refuses in an agent's box; `make loop` passes the
  flags with `VALIDATE_SLACK` (default 0); it needs hi 0.52.0.
  `autoresearch-base`'s agent notes describe the validation folder. The
  private quant template got the same on 2026-10-12, in its own
  repository: 2019 to 2022 as its validation window, and `make robust`
  suggesting a slack from the in-sample tranche spread scaled to the
  window's length.
