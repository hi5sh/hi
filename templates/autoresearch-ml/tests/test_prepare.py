import itertools
import math
from pathlib import Path

import pytest
import torch
from torch import nn

import prepare
from prepare import Vocabulary


class Uniform(nn.Module):
    """Predicts every character equally: exactly log2(vocab) bits each."""

    def __init__(self, vocab_size: int) -> None:
        super().__init__()
        self.vocab_size = vocab_size

    def forward(self, ids: torch.Tensor) -> torch.Tensor:
        return torch.zeros(*ids.shape, self.vocab_size)


def test_the_vocabulary_round_trips() -> None:
    vocabulary = Vocabulary("\n abc")
    ids = vocabulary.encode("cab a\n")
    assert ids.tolist() == [4, 2, 3, 1, 2, 0]
    assert vocabulary.decode(ids) == "cab a\n"


def test_a_uniform_model_scores_log2_of_the_vocabulary() -> None:
    data = torch.randint(0, 50, (prepare.BLOCK * 3 + 1,))
    assert prepare.bits_per_char(Uniform(50), data, 50, torch.device("cpu")) == pytest.approx(
        math.log2(50)
    )


def test_a_model_of_the_wrong_shape_is_refused() -> None:
    data = torch.zeros(prepare.BLOCK * 2 + 1, dtype=torch.long)
    with pytest.raises(ValueError, match="shape"):
        prepare.bits_per_char(Uniform(10), data, 12, torch.device("cpu"))


def test_validation_and_holdout_are_kept_out_of_eval(tmp_path: Path) -> None:
    text = "".join(chr(ord("a") + i % 26) for i in range(1000))
    holdout = tmp_path / "holdout" / "test.txt"
    validation = tmp_path / "validation" / "validation.txt"
    prepare.save(text, tmp_path / "eval", holdout, validation)
    train = (tmp_path / "eval" / "train.txt").read_text()
    val = (tmp_path / "eval" / "val.txt").read_text()
    sizes = (len(train), len(val), len(validation.read_text()), len(holdout.read_text()))
    assert sizes == (700, 100, 100, 100)
    assert train + val + validation.read_text() + holdout.read_text() == text
    assert sorted(path.name for path in (tmp_path / "eval").iterdir()) == [
        "split.json",
        "train.txt",
        "val.txt",
        "vocab.json",
    ]
    assert len(Vocabulary.load(tmp_path / "eval")) == 26


def test_folds_walk_the_validation_window_forward(tmp_path: Path) -> None:
    text = "".join(chr(ord("a") + i % 26) for i in range(1000))
    holdout = tmp_path / "holdout" / "test.txt"
    validation = tmp_path / "validation" / "validation.txt"
    seen = []
    for fold in (1, 2, 3):
        prepare.save(text, tmp_path / "eval", holdout, validation, folds=3, fold=fold)
        train = (tmp_path / "eval" / "train.txt").read_text()
        val = (tmp_path / "eval" / "val.txt").read_text()
        seen.append((train + val, validation.read_text()))
        assert (len(train), len(val), len(validation.read_text())) == (
            500 + 100 * (fold - 1),
            100,
            100,
        )
        assert holdout.read_text() == text[900:]
        assert prepare.folds_of(tmp_path / "eval") == 3
    # Each fold's validation block joins what agents see in the next, and
    # the last one ends where the holdout starts.
    for (agents, block), (next_agents, _) in itertools.pairwise(seen):
        assert next_agents == agents + block
    assert seen[-1][0] + seen[-1][1] == text[:900]


def test_too_many_folds_are_refused() -> None:
    with pytest.raises(SystemExit, match="nothing to train on"):
        prepare.bounds(1000, folds=8)
    with pytest.raises(SystemExit, match="fold 4"):
        prepare.bounds(1000, folds=3, fold=4)
