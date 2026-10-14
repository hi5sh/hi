# Hifin Template Name

Autoresearch on model training, after Karpathy's
[autoresearch](https://github.com/karpathy/autoresearch). Agents change
`train.py`; every attempt trains for the same five minutes and is scored on
text it never saw; `hi agent best-of` keeps a change only when the score
beats the best so far *and* it holds on validation text no agent can see,
and commits it.

| File | |
|---|---|
| `train.py` | the model and its training; the only file agents change during a run |
| `evaluate.py` | trains for the time budget, refuses longer runs, prints `val_bpc:` last |
| `prepare.py` | the text, its four-way split, the time budget, and the measurement |
| `eval/` | the frozen text agents train and score on (`make data` writes it) |
| `program.md` | the task the agents get |

```sh
make data       # download the text; validation and holdout go outside the repo
make score      # five minutes of training, then the baseline val_bpc
git add -A && git commit -m "Data and baseline"
make loop       # rounds in the background; hi agent best-of watch b-1
make holdout    # once, at the end, on the gains' branch
```

## Before a run

- **Data:** `URL` and `SHA256` in `prepare.py`, or your own `download()`.
  The example is Tiny Shakespeare, character by character, split four ways:

  | Part | Where | Who sees it |
  |---|---|---|
  | 70% train, 10% val | `eval/`, committed | agents; `make score` reports `val_bpc` |
  | 10% validation | `../<folder>-validation/` | only hi, after each round (`make validate`) |
  | 10% holdout | `../<folder>-holdout/` | only you, once, at the end (`make holdout`) |
- **Budget:** `TIME_BUDGET` in `prepare.py`, five minutes. Shorter means
  more rounds a night and noisier scores.
- **Model:** the example in `train.py` is a small GPT; start from yours.
- **GPU:** on a Strix Halo, `make sync` (run by `hi init`) gives boxes
  AMD's PyTorch build and the GPU, in `.devcontainer/devcontainer.json`.
  With the GPU, hi scores boxes one at a time, so they don't share it while
  the clock runs.
- **Run:** the `loop` target in the `Makefile` holds the flags: boxes per
  round, rounds, budget, `--edit train.py`, and the validation gate.

## The validation gate

Over many rounds, agents fit `eval/val.txt`: every kept change was chosen
because it scored well there. `make loop` therefore passes
`--validate "make validate" --validate-data ../<folder>-validation`:

- After each round, hi copies the gains' tip, puts in only the round's
  `train.py`, and runs `make validate` on it in a container with no network
  and no `.git`, with the validation text read-only at `/box/data`. Agents'
  boxes never get that folder, and `make validate` in a box refuses.
- The round's best `train.py` (chosen on `val_bpc` alone) is kept only if
  its `validation_bpc` is no worse than the best (lowest) so far, by more than
  `VALIDATE_SLACK` (0 by default). Otherwise the round is `rejected`.
- Agents see only `rejected` in the next round's history, never the
  numbers. `hi agent best-of watch` and `show` have them (`VALID.`).
- `make validate` trains again, so each round takes about one more time
  budget per box that passed; with the GPU, one box at a time.
- Set `VALIDATE_SLACK` to about the score's noise: run `make validate`
  twice and see how far the two numbers are apart. With 0, a change that
  is neutral on the validation text fails about half the time.

## Folds

One validation window wears out over a long run. With `FOLDS` above 1, it
walks forward instead (hi 0.54.0 or later):

```sh
make data FOLDS=3 && git add eval && git commit -m "Data, fold 1 of 3"
make loop FOLDS=3 FOLD_EVERY=10   # without FOLD_EVERY, a fold ends when --patience runs out
```

| Fold | Agents (`eval/`) | Validation |
|---|---|---|
| 1 | the first 60% (50% train, 10% val) | the next 10% |
| 2 | the first 70% | the next 10% |
| 3 | the first 80%, the usual split | the 10% before the holdout |

Between folds, hi runs `make fold` in a worktree of the gains' tip, commits
the new `eval/` to the gains' branch, and measures the tip again as the
fold's baseline. The holdout never moves. `make data` keeps the whole text
beside the holdout (`text.txt`), so a fold needs no network.

## After a run

The gains are commits on `best-of/<run>/best`:

```sh
git checkout best-of/b-1/best && make holdout && git checkout -
git merge best-of/b-1/best
```

Scores move a little from run to run, as training does; a gain smaller than
that noise isn't one. `--min-gain` in the `loop` target tells hi to ignore
such gains.
