---
title: Keep only gains that hold
description: hi agent best-of --validate checks every round's winner on data the agents never see, in a container of its own, and keeps a gain only if it holds there too. The flags, how it works, what agents see, choosing the slack, and troubleshooting.
---

Rounds keep whatever scores best. Over tens of rounds and several boxes a
round, that search fits the data the score reads: changes that suit its
quirks win, whether or not they would work anywhere else. The holdout
catches this, but only once, at the end, when the run is over.

In the first long run of the quant template, the in-sample Sharpe rose from
0.86 to 1.53 while the holdout Sharpe *fell*, from 0.94 to 0.87. Nothing
during the run could tell.

`--validate` gives the run a second test. The data is split three ways:

| Part | Who sees it | What it is for |
|---|---|---|
| Score data, in the repository | agents, all the time | the score they improve |
| Validation data, outside the repository | only hi, after each round | the gate: a gain must not lose here |
| Holdout, outside the repository | only you, once, at the end | the final word |

A round's winner is kept only if it beats the best score **and** its
validation number is no worse than the highest one so far.

```sh
hi agent best-of 3 --rounds 20 --score "make score" --higher --edit strategy.py \
  --validate "make validate" --validate-data ../quant-validation --validate-slack 0.05 program.md
```

In the [templates](/guide/autoresearch/templates/), `make loop` passes
these flags for you.

## The flags

They go with `--score`, and `--validate` needs `--edit`.

### `--validate "<command>"`

The command that measures a result on the hidden data, such as
`--validate "make validate"`.

- It works like `--score`: hi takes **the last number it prints**, and
  better is the same direction as the score (`--lower` or `--higher`).
- The difference is where it runs. `--score` runs inside the agent's box.
  `--validate` runs after the round, in a container hi builds itself (see
  [How it works](#how-it-works)).
- A validation that fails, times out, or prints no number counts as not
  holding: that box can't be kept.

### `--validate-data <dir>`

The folder of hidden data, such as `--validate-data ../quant-validation`.

- It is on your machine, outside the project, so it is in no commit and in
  no agent's worktree. hi refuses a folder inside the project.
- hi mounts it **read-only at `/box/data`**, in validation containers only.
  Agents' boxes never get it.
- Your validation command reads it from there; the templates' `make
  validate` does.
- Without it, the command runs on what the project has, which helps only
  when the validation is hidden some other way. The templates always pass
  a folder.

### `--validate-slack <number>`

How much lower than the highest so far the validation number may be and
still pass. "The highest so far" is the best validation number of the
baseline and every kept version. The default is 0: no lower at all.

- With 0, a change that is truly neutral on the hidden data fails about
  half the time, from noise alone. Set the slack to about the validation
  number's noise (see [Choosing the slack](#choosing-the-slack)).
- It is in the score's own units, like `--min-gain`.
- Example, higher is better: the highest validation so far is 0.81 and the
  slack is 0.05. A winner at 0.78 passes (0.03 lower) and is kept, but 0.81
  stays the mark: a later winner at 0.77 still passes (0.04 lower), one at
  0.75 doesn't (0.06 lower), however close it is to 0.78. A winner at 0.90
  passes and 0.90 becomes the new mark.

### Why `--validate` needs `--edit`

To build the validation container, hi takes the harness, the `Makefile`,
and the data from the gains' branch, and only the agent's own files from
its box. `--edit` (such as `--edit strategy.py`) is what tells hi which
files are the agent's. Without it, hi would have to copy everything,
including an evaluation the agent might have changed.

## How it works

Validation never runs in an agent's box. The code being validated is the
agent's own, and in its box the agent could have prepared a way out: the
project's `.git` is writable there, `--edit` is checked only after the
score has run, and the box's proxy reaches the agent's model. So:

1. **Agents' boxes are unchanged.** They work, check, and score as without
   `--validate`. The validation data is never mounted in them.
2. **The baseline is validated first.** After the baseline score, hi
   validates your last commit. A validation that fails or prints no number
   stops the run there, before it costs tokens.

   ```text
   Measuring the baseline on 3f2a91c0e4b1.
   Baseline: 0.86, in 41s.
   Validating the baseline: make validate, in a container without network.
   Baseline validation: 0.79, in 38s.
   ```

3. **After each round, a fresh container per good box.** When the round's
   boxes are scored, hi validates every box whose result could be kept
   (check passed, a score, inside `--edit`, something changed). For each
   it builds a folder outside the project:
   - the gains' branch as it is (`git archive`), so the harness, the
     `Makefile`, and the data are hi's copies, whatever the agent did to its
     own;
   - with only the files the agent changed, all inside `--edit`, taken
     from the box's snapshot.

   It runs the project's box image on that folder with **no network**, **no
   proxy**, **no `.git`**, nothing from the agent's box, the data read-only
   at `/box/data`, and the folder at `/box/project`. The command runs under
   a time limit of twice the baseline validation's time plus a minute. The
   container and its folder are removed when it exits.
4. **The gate.** The round's winner is chosen on the score alone, as
   without `--validate`. It is kept only if its score beats the best by
   more than `--min-gain` *and* its validation number is no lower than the
   highest so far (the baseline's or any kept version's) by more than
   `--validate-slack`. Otherwise the round is **`rejected`**.

hi doesn't fall back to the second-best box when the winner is rejected:
choosing among boxes by their validation numbers would fit the validation
data too.

The gate compares with the highest validation number so far, not the latest
kept version's. Against the latest, each kept round could lose up to the
slack, and the losses would add up: ten rounds at a slack of 0.05 could
lose 0.5. Against the highest, validation can never end more than the slack
below its best point, and it doesn't have to rise every round either. (In
v0.52.0 the gate compared with the latest. A run started then, and resumed
after an update, starts its mark at its latest kept number, since the
earlier ones weren't recorded, and keeps the highest from there.)

Each good box is validated, not just the winner, so the report shows every
attempt's number. Without the GPU the containers run at once; with
`--gpu`, one at a time, like the scores.

### In the container

| | |
|---|---|
| Working folder | `/box/project`: the gains' branch plus the box's `--edit` files |
| Data | `/box/data`, read-only: `--validate-data` |
| Network | none |
| `.git` | none |
| Environment | `HI_VALIDATE=1`, `HI_BOX` (so a `Makefile` picks the image's tools), `HOME=/box/home`, and the project's `containerEnv` |
| Image | the one the run's boxes use (`.devcontainer/`, `--image`, or hi's) |
| Time limit | twice the baseline validation's time plus a minute |

The image must hold everything the command needs, since there is no
network: the templates' images have their dependencies in `/opt/venv`. A
project whose packages are fetched when a box starts fails at the
baseline validation, before any agent runs. The devcontainer's
`postCreateCommand` doesn't run in validation containers.

## What agents see

Validation numbers and data are in nothing a box can read:

- **The next round's task** shows each attempt's score and result
  (`kept`, `discarded`, `rejected`, `crash`), never a validation number. It
  says that hi also checks the round's best on data that isn't in the box.
- **Commits** on the gains' branch and the attempt refs under
  `refs/best-of/<run>/`, which boxes can read through the project's `.git`,
  carry no validation number.
- **The data** reaches only validation containers.

`rejected` tells agents one bit: a change that raised the score didn't hold
up. That is the point of the gate; they learn which kinds of ideas fail, not
the numbers.

The numbers are in the run's state, outside every box: `hi agent best-of
watch`, `show`, `show --json`, `ls --json`, and the run's `loop.log`, so a
`--then` command can report them. Don't commit a report with validation
numbers to the gains' branch and then `resume`: the next boxes could read
it through `.git`.

## The view

```text
Best-of b-9 · "program.md" · 3 boxes a round · round 7 of 20 · 1h12m · $9.40 at API prices
Score: make score (higher is better) · validated by: make validate
Best: 1.12 (validation 0.81) from round 4, baseline 0.86 (validation 0.79) · on best-of/b-9/best

  ROUND  RESULT     SCORE  VALID.  BOX            IDEA
  4      kept       1.12   0.81    quant-b9-r4-1  inverse-volatility weights
  5      rejected   1.31   0.62    quant-b9-r5-3  six lookbacks fitted per asset
  6      discarded  1.09   –       quant-b9-r6-2  add a crash filter

Now: round 7: validating 2 boxes
```

`VALID.` is the round winner's validation number. `show --json` has every
box's under `validation` (its value, exit status, seconds, and the last
lines of its output) and the run's `baseline_validation`,
`best_validation` (the latest kept version's), and `peak_validation` (the
highest so far, which the gate compares with). With folds it also has
`fold`, `folds`, and `fold_history`: each change's fold, the round after
which it came, its commit, and the new baseline's score and validation.

A rejected round counts as a round without a gain for `--patience`.

## Folds: a window that stays fresh

One validation window wears out: every gate it decides is a little
selection on it. With `--folds`, the run walks forward through several
windows, and agents get more data as it goes.

```sh
hi agent best-of 3 --rounds 60 --score "make score" --higher --edit strategy.py \
  --validate "make validate" --validate-data ../quant-validation \
  --folds 3 --fold "make fold" --fold-every 20 program.md
```

| Flag | Meaning |
|---|---|
| `--folds <n>` | the run moves through n folds, from fold 1; needs `--validate` |
| `--fold "<command>"` | moves to the next fold; hi runs it on this machine |
| `--fold-every <k>` | move on after k rounds; without it, when `--patience` runs out |

When a fold ends, hi:

1. runs `--fold` in a temporary worktree of the gains' tip, with `HI_FOLD`
   (the new fold's number), `HI_FOLDS`, `HI_PROJECT` (your checkout), and
   `HI_VALIDATE_DATA` (the validation folder) set. The command moves the
   window: the old validation block joins the data in the project, and the
   next block becomes the validation folder. It must not change the
   `--edit` files; if it does, the run stops.
2. commits what it changed to the gains' branch, with the message
   `best-of <run> fold <n>` and nothing else;
3. scores the tip in a box and validates it, as the fold's new baseline.
   The best so far is measured on the new windows, not carried over: the
   next round has to beat the tip on the new data;
4. starts `--patience` again, and goes on.

Agents see a `fold` row in their history, with the new baseline's score,
never its validation. `watch` and `show` show `fold 2 of 3` in the header
and a row for each fold change. With `--patience` and folds, the run ends
when patience runs out in the last fold.

The last fold's validation block ends where the holdout starts, so no fold
touches the holdout. A run that ends early stays in its fold, and `resume`
goes on from there. If a fold change fails part way, `resume` runs it
again: write `--fold` so that it writes the windows for `HI_FOLD`, and a
second run changes nothing.

In `autoresearch-ml`, set `FOLDS` (and `FOLD_EVERY`, if you like) before
`make data`, which writes fold 1:

```sh
make data FOLDS=3 && git add eval && git commit -m "Data, fold 1 of 3"
make loop FOLDS=3 FOLD_EVERY=10
```

Fold 1 gives agents 60% of the text and validates on the next 10%; each
fold moves 10% into `eval/`. Fold 3 is the usual 70/10/10/10 split.

### Noise, slack, and results with folds

Folds change how much data each number rests on, so set the noise
thresholds for them before the run:

- **Measure the noise once, on fold 1, right after `make data FOLDS=n`.**
  Fold 1 is what your checkout holds, and its numbers are the noisiest of
  the run: it has the least data agents see. A `--min-gain` measured there
  is a little larger than later folds need. That errs on the safe side:
  a few small real gains are ignored, but no noise is kept.
- **Make the windows the same length.** Then one `--validate-slack` fits
  every fold: the slack is about the noise of the validation number, and
  that depends on the window's length, not on where it sits. Both
  templates do this.
- **A shorter window needs a larger slack.** Splitting the same years into
  more windows makes each one shorter, and a number measured on a shorter
  window moves more by chance. A Sharpe ratio's noise grows as one over the
  square root of the years: a two-year window is about 1.4 times noisier
  than a four-year one. Keep the slack you used without folds and the gate
  rejects real gains, often. In the quant template, `make robust` suggested
  0.20 for its two-year folds, against the 0.05 default for four years.
- **Results are measured on one set of windows.** A results command that
  re-scores every attempt, such as the quant template's `make results`, uses
  the windows of the fold the run ended in. Attempts from earlier folds were
  scored on other windows, so their recorded numbers won't match the new
  ones. That is expected, not a bug: comparing every attempt on the same
  windows is the point of the report.

## Choosing the slack

1. Run the validation twice on the same code, on your machine:
   `make validate`, twice. For a deterministic backtest the two numbers are
   equal and the noise is in the data instead: the quant template's `make
   robust` suggests a slack from the spread of the validation window's
   tranches, as it does for `--min-gain`.
2. Set `--validate-slack` to about that difference. Too small rejects real
   gains; too large lets fitted changes through.
3. In the templates, it is `VALIDATE_SLACK` in the `Makefile` or on the
   command line: `make loop VALIDATE_SLACK=0.01`.
4. With [folds](#noise-slack-and-results-with-folds), measure again after
   `make data FOLDS=n`: each window is shorter, so its slack is larger.

## Costs

- **Time.** Each round adds one validation per good box. For a backtest
  that is seconds. For the ML template it is another training run per box,
  since `make validate` trains `train.py` again before it scores; with the
  GPU, one box at a time.
- **Data.** The validation window comes out of what agents used to see:
  less to fit, and noisier numbers if the window is short.
- **The gate wears out slowly.** Each gate is a question asked of the
  validation data: a run of 20 rounds asks about 20, against the hundreds of
  scores agents see. [Folds](#folds-a-window-that-stays-fresh) spread the
  questions over several windows. The holdout stays the final word.
- **Folds.** Each fold change scores and validates the tip once more.

## After a run

```sh
hi agent best-of show b-9                          # every round's score and validation
git checkout best-of/b-9/best && make holdout && git checkout -
```

If the holdout moved with the validation numbers, merge. If validation held
and the holdout still fell, the window was too short or too much like the
score's data; lengthen it before the next run.

## Troubleshooting

| You see | It means |
|---|---|
| `--validate needs --edit` | Name the agents' files: `--edit strategy.py`. |
| `--validate-data … is inside the project` | Move the folder next to the project, such as `../<folder>-validation`, and pass that. |
| `the validation doesn't work on your last commit` | The command failed or printed no number in the container. Its output follows; the usual causes are a package the image lacks (no network there) or a path other than `/box/data`. |
| Every round `rejected` | The slack is too tight for the noise, or the agents are fitting the score's data. Try a slack of about the noise; tell agents in the task that fitted ideas are rejected. |
| `make validate` in a box says the text stays outside | As it should: agents can't run it. hi runs it in its own container. |
| `the validation ran out of time` | It took more than twice the baseline validation's time plus a minute. |
| `say when a fold ends` | `--folds` needs `--fold-every <k>` or `--patience <n>`. |
| `fold 2: make fold changed strategy.py, inside --edit` | The fold command may move the data, never the agents' files. Fix it, then `hi agent best-of resume <run>`. |
| `eval/ wasn't written for 3 folds` | `make data FOLDS=3`, commit `eval/`, then `make loop FOLDS=3`. |

The design and its reasons are in the
[specification](https://github.com/hi5sh/hi/blob/main/docs/specs/approved/hi_agent_best_of_validate.md).
