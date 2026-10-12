---
title: Advanced examples
description: Longer hi agent best-of runs - overnight model training on a Strix Halo from the autoresearch-ml template, a safe speed-up with a noisy benchmark, and extending a run.
---

These start from the [autoresearch template](/guide/autoresearch/templates/)
or follow their pattern: a fixed evaluation, one file agents may change,
and a holdout outside the repository.

## Train a better model overnight on a Strix Halo

The aim: the lowest validation loss after exactly five minutes of training,
improved by agents all night, on the workstation's GPU.

```sh
hi init autoresearch-ml charlm && cd charlm
```

`hi init` runs `make sync`, which on a Strix Halo installs AMD's PyTorch
build and sets the box image to it, with the GPU.

**Adapt it.** To train on your own text, change `URL` and `SHA256` in
`prepare.py` (or write your own `download()`), and put the model you start
from in `train.py`. Then:

```sh
make data          # eval/ (70% train, 10% val); 10% to ../charlm-validation, 10% to ../charlm-holdout
make score         # the baseline: five minutes of training, then val_bpc
git add -A && git commit -m "Data and baseline"
```

Run `make score` twice, and `make validate` twice. The differences are the
noise: use the first as the gain hi should ignore (`--min-gain`) and the
second as the slack the validation gate allows (`--validate-slack`).

**Run it.** Edit the `loop` target, or pass the flags yourself:

```sh
hi agent best-of 3 --agents claude,codex --for 9h --budget 60 --patience 15 \
  --min-gain 0.005 --max 30m --score "make score" --lower --edit train.py \
  --validate "make validate" --validate-data ../charlm-validation --validate-slack 0.005 program.md
```

- Three agents work at once; then hi scores the three boxes one after
  another on the GPU, five minutes each, so the clock is fair.
- After scoring, hi trains each good box's `train.py` once more and scores
  it on `../charlm-validation`, in a container with no network. The round's
  best is kept only if that number is no worse than the highest so far,
  within the slack ([how it works](/guide/autoresearch/validation/)).
- A round takes the agents' time plus about 18 minutes of scoring and up to
  as much again for validation; nine hours is about 10 to 15 rounds.
- `--patience 15` ends a run that has stopped improving; `--budget 60`
  caps the spend.

```sh
hi agent best-of watch b-1    # q leaves; the run goes on through the night
```

**In the morning:**

```sh
hi agent best-of show b-1
git log --oneline main..best-of/b-1/best    # one commit per gain, with its idea and score
git checkout best-of/b-1/best && make holdout && git checkout -
```

`show` has each round's `val_bpc` and its validation number (`VALID.`),
and which rounds were `rejected`. If the holdout score improved about as
much as the validation numbers, merge. If they improved and the holdout
didn't, the gains fit both windows; keep only the early commits, or none,
and lengthen the validation window next time.

## A speed-up with a noisy benchmark

The aim: faster code, the same results, in a project that isn't a template.

1. **A score that's stable.** Make `make bench` print the median of several
   runs, and run it ten times. If it moves by 3%, a gain under 3% is noise.
   `--min-gain` is in the score's own units: 3% of a 0.7-second baseline is
   about 0.02.
2. **A gate that's strict.** `make check` with the tests that pin the
   output, so a faster wrong answer fails before it is scored.
3. **A fence.** `--edit src/`: results that change the benchmark, the tests,
   or the `Makefile` are discarded.

```sh
hi agent best-of 3 --agents claude,codex --rounds 12 --budget 25 \
  --check "make check" --score "make bench" --lower --min-gain 0.02 \
  --edit src/ "make the backtest engine faster; results must stay byte-identical"
```

Benchmarks with a fixed workload don't overfit the way models do, so no
holdout is needed; the check is what keeps results right.

## Extend, stop, and start again

```sh
hi agent best-of stop b-1                 # finish this round, then stop
hi agent best-of resume b-1 --rounds 10   # ten more, from the best so far
hi agent best-of resume b-1 --for 4h --budget 20
hi agent best-of stop b-1 --now           # stop at once; the round's boxes are removed
```

A resumed run keeps its gains' branch, its history, and its spend; agents
still see every earlier attempt. To start over from what a run found, merge
its branch and start a new run: its baseline is the merged result.
