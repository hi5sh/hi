"""The data and the measurement: which text, how it is split, how long a model
trains, and how it is scored. Don't change it during a run.

Run it on this machine, not in a box, once before a run (it needs the
network):

    make data

eval/train.txt, eval/val.txt    the first 80% of the text, committed: models
                                train on the first and are scored on the second
                                (make score, the number agents see).
eval/vocab.json                 the characters, in the order of their ids.
../<folder>-validation/validation.txt
                                the next 10%, outside the repository: hi scores
                                each round's best result on it (make validate)
                                in a container no agent can reach, and keeps a
                                gain only if it holds there too.
../<folder>-holdout/test.txt    the last 10%, outside the repository, so no
                                agent and no round sees it; make holdout scores
                                on it, once, at the end. text.txt beside it is
                                the whole text, which make fold splits again.

With FOLDS above 1, the validation window walks forward during a run, so it
doesn't wear out: fold 1 leaves agents less text, and each fold (make fold,
which hi runs between folds) moves the validation block into eval/ and the
next block out of it. The last fold is the split above, and its validation
block ends where the holdout starts.

The example is Tiny Shakespeare (1.1 MB, public domain), modelled character
by character, as in Karpathy's char-rnn and nanoGPT. Change URL and SHA256,
or replace download(), before a run, never during one.
"""

import argparse
import hashlib
import json
import math
import os
import urllib.request
from pathlib import Path

import torch
from torch.nn import functional

URL = "https://raw.githubusercontent.com/karpathy/char-rnn/master/data/tinyshakespeare/input.txt"
SHA256 = "86c4e6aa9db7c042ec79f339dcb96d42b0075e16b8fc2e86bf0ca57e2dc565ed"
SPLITS = (0.7, 0.1, 0.1)  # train, val, and validation; the rest is the holdout
TIME_BUDGET = 300.0  # seconds of training for every score
BLOCK = 256  # the context length models are scored at

HERE = Path(__file__).resolve().parent
EVAL = HERE / "eval"
HOLDOUT = Path(os.environ.get("HOLDOUT") or HERE.parent / f"{HERE.name}-holdout" / "test.txt")
VALIDATION = Path(
    os.environ.get("VALIDATION") or HERE.parent / f"{HERE.name}-validation" / "validation.txt"
)


class Vocabulary:
    """Characters and their ids."""

    def __init__(self, characters: str) -> None:
        self.characters = characters
        self.ids = {character: i for i, character in enumerate(characters)}

    @classmethod
    def load(cls, folder: Path = EVAL) -> Vocabulary:
        try:
            return cls("".join(json.loads((folder / "vocab.json").read_text())))
        except FileNotFoundError:
            raise SystemExit(f"there is no {folder / 'vocab.json'}; make data writes it") from None

    def __len__(self) -> int:
        return len(self.characters)

    def encode(self, text: str) -> torch.Tensor:
        return torch.tensor([self.ids[character] for character in text], dtype=torch.long)

    def decode(self, ids: torch.Tensor) -> str:
        return "".join(self.characters[i] for i in ids.tolist())


def download() -> str:
    with urllib.request.urlopen(URL, timeout=60) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != SHA256:
        raise SystemExit(f"{URL} isn't the file SHA256 names; check it, then update SHA256")
    return data.decode("utf-8")


def source(holdout: Path = HOLDOUT) -> str:
    """The whole text: kept beside the holdout after the first download, so
    each fold splits the same text without the network."""
    kept = holdout.parent / "text.txt"
    if kept.exists() and hashlib.sha256(kept.read_bytes()).hexdigest() == SHA256:
        return kept.read_text()
    text = download()
    kept.parent.mkdir(parents=True, exist_ok=True)
    kept.write_text(text)
    return text


def bounds(length: int, folds: int = 1, fold: int = 1) -> tuple[int, int, int, int]:
    """Where train, val, and validation end, and where the holdout starts, for
    a fold: each fold before the last ends one validation block earlier."""
    train, val, validation = SPLITS
    if not 1 <= fold <= folds:
        raise SystemExit(f"fold {fold} isn't between 1 and {folds}")
    if train - (folds - 1) * validation <= 0:
        raise SystemExit(f"{folds} folds leave nothing to train on in the first")
    shift = (folds - fold) * validation
    train_end, val_end, validation_end = (
        round(length * end) for end in (train - shift, train + val - shift, sum(SPLITS) - shift)
    )
    return train_end, val_end, validation_end, round(length * sum(SPLITS))


def save(
    text: str,
    folder: Path = EVAL,
    holdout: Path = HOLDOUT,
    validation: Path = VALIDATION,
    folds: int = 1,
    fold: int = 1,
) -> None:
    train_end, val_end, validation_end, holdout_start = bounds(len(text), folds, fold)
    folder.mkdir(parents=True, exist_ok=True)
    (folder / "vocab.json").write_text(json.dumps(sorted(set(text))) + "\n")
    (folder / "split.json").write_text(json.dumps({"folds": folds, "fold": fold}) + "\n")
    (folder / "train.txt").write_text(text[:train_end])
    (folder / "val.txt").write_text(text[train_end:val_end])
    for path, part in (
        (validation, text[val_end:validation_end]),
        (holdout, text[holdout_start:]),
    ):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(part)
    if folds > 1:
        print(f"fold {fold} of {folds}")
    print(f"{folder}: {train_end} characters to train on, {val_end - train_end} to score on")
    print(f"{validation}: {validation_end - val_end} characters that gate each round's gain")
    print(f"{holdout}: {len(text) - holdout_start} characters for the end")


def load(path: Path, vocabulary: Vocabulary) -> torch.Tensor:
    try:
        return vocabulary.encode(path.read_text())
    except FileNotFoundError:
        raise SystemExit(f"there is no {path}; make data writes it") from None


def bits_per_char(
    model: torch.nn.Module, data: torch.Tensor, vocab_size: int, device: torch.device
) -> float:
    """The model's average cross-entropy on data, in bits per character: the
    text cut into blocks of BLOCK characters, each predicting the next."""
    blocks = (len(data) - 1) // BLOCK
    if blocks == 0:
        raise ValueError(f"the text is shorter than one block of {BLOCK} characters")
    inputs = data[: blocks * BLOCK].view(blocks, BLOCK)
    targets = data[1 : blocks * BLOCK + 1].view(blocks, BLOCK)
    model.eval()
    total = 0.0
    with torch.no_grad():
        for start in range(0, blocks, 16):
            batch = inputs[start : start + 16].to(device)
            logits = model(batch)
            if tuple(logits.shape) != (*batch.shape, vocab_size):
                raise ValueError(
                    f"model(ids) returned shape {tuple(logits.shape)}, "
                    f"not (batch, {BLOCK}, {vocab_size})"
                )
            loss = functional.cross_entropy(
                logits.float().reshape(-1, vocab_size),
                targets[start : start + 16].to(device).reshape(-1),
                reduction="sum",
            )
            total += float(loss)
    return total / (blocks * BLOCK) / math.log(2)


def folds_of(folder: Path = EVAL) -> int:
    """How many folds eval/ was written for; 1 before folds existed."""
    try:
        return int(json.loads((folder / "split.json").read_text())["folds"])
    except FileNotFoundError:
        return 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Write the data: make data, or make fold")
    parser.add_argument("--folds", type=int, default=1, help="how many folds the run walks through")
    parser.add_argument("--fold", type=int, default=1, help="the fold to write (hi's HI_FOLD)")
    options = parser.parse_args()
    if options.fold > 1 and folds_of() != options.folds:
        raise SystemExit(
            f"eval/ was written for {folds_of()} folds, not {options.folds}; "
            f"make data FOLDS={options.folds} before the run"
        )
    save(source(), folds=options.folds, fold=options.fold)
