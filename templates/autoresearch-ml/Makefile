# hi:begin: written by hi init; change it in the template, not here
-include local.mk
# The PyTorch build for this machine: rocm on a Strix Halo (gfx1151), cuda
# with an NVIDIA GPU, cpu otherwise. To choose another, set TORCH in the
# environment or in local.mk, which Git ignores: TORCH = cpu
TORCH ?= $(shell if grep -qs 'gfx_target_version 110501' /sys/class/kfd/kfd/topology/nodes/*/properties; then echo rocm; elif command -v nvidia-smi >/dev/null 2>&1; then echo cuda; else echo cpu; fi)
# In a box (hi agent best-of), the tools come from the box image's /opt/venv;
# on this machine, uv runs them from the lockfile with this machine's build.
ifdef HI_BOX
RUN := env PATH=/opt/venv/bin:$(PATH)
LOCK_CHECK := true
else
RUN := uv run --frozen --no-default-groups --group dev --group $(TORCH)
LOCK_CHECK := uv lock --check
endif
# Boxes run AMD's build with the GPU on a Strix Halo, and the CPU build elsewhere.
BOX_TORCH := $(if $(filter rocm,$(TORCH)),rocm,cpu)
.DEFAULT_GOAL := help
.PHONY: help check fix lint test sync

help: ## Show the targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'
	@echo "  PyTorch build here: $(TORCH)"

sync: ## Install the dependencies with this machine's PyTorch build; the first time, set the box image's
	uv sync --no-default-groups --group dev --group $(TORCH)
	@if grep -q '"TORCH": "auto"' .devcontainer/devcontainer.json; then \
	  sed -i 's/"TORCH": "auto"/"TORCH": "$(BOX_TORCH)"/' .devcontainer/devcontainer.json; \
	  if [ "$(BOX_TORCH)" = rocm ]; then sed -i 's/"gpu": false/"gpu": true/' .devcontainer/devcontainer.json; fi; \
	  echo "Boxes get the $(BOX_TORCH) build of PyTorch (.devcontainer/devcontainer.json)."; \
	fi

check: ## Lockfile, format, lint, types, and tests; must pass before a change is done
	$(LOCK_CHECK)
	$(RUN) ruff format --check
	$(RUN) ruff check
	$(RUN) pyrefly check
	$(RUN) pytest -q

fix: ## Format and fix what ruff can
	$(RUN) ruff format
	$(RUN) ruff check --fix

lint: ## Format check and lint only
	$(RUN) ruff format --check
	$(RUN) ruff check

test: ## Tests only
	$(RUN) pytest -q
# hi:end

# Project targets below.
.PHONY: score validate holdout data fold loop

# The project's folder: between folds, hi runs make fold in a worktree of its
# own and names your checkout in HI_PROJECT, and the validation folder in
# HI_VALIDATE_DATA.
PROJECT := $(or $(HI_PROJECT),$(CURDIR))
HOLDOUT ?= $(abspath $(PROJECT)/..)/$(notdir $(PROJECT))-holdout/test.txt
VALIDATION_DIR ?= $(or $(HI_VALIDATE_DATA),$(abspath $(PROJECT)/..)/$(notdir $(PROJECT))-validation)
# How many folds the validation window walks through during a run, so it
# doesn't wear out (1: it stays put). make data writes fold 1; set it before
# make data, and move on after FOLD_EVERY rounds, or when --patience runs out.
FOLDS ?= 1
FOLD_EVERY ?=
# How much worse than the best so far a round's validation_bpc may be and still be
# kept; about the noise of a score, which you can see by running make
# validate twice.
VALIDATE_SLACK ?= 0
BOXES ?= 2
ROUNDS ?= 20
BUDGET ?= 20

score: ## Train for prepare.TIME_BUDGET and score; the last line is the number the loop lowers
	@$(RUN) python evaluate.py

validate: ## Train, then score on the validation text; hi runs it after each round, in a container agents can't reach
	@if [ -n "$(HI_VALIDATE)" ]; then $(RUN) python evaluate.py --validation /box/data/validation.txt; \
	elif [ -n "$(HI_BOX)" ]; then echo "the validation text stays outside agents' boxes"; exit 1; \
	else $(RUN) python evaluate.py --validation $(VALIDATION_DIR)/validation.txt; fi

holdout: ## Train, then score on the text outside the repository; once, at the end
	@test -z "$(HI_BOX)" || { echo "the holdout stays outside boxes"; exit 1; }
	@$(RUN) python evaluate.py --holdout $(HOLDOUT)

data: ## Download the text: eval/ in the repository, the holdout outside it
	HOLDOUT=$(HOLDOUT) VALIDATION=$(VALIDATION_DIR)/validation.txt $(RUN) python prepare.py --folds $(FOLDS)

fold: ## hi runs it between folds, with HI_FOLD: the validation block joins eval/, the next one leaves it
	@test -z "$(HI_BOX)" || { echo "the data moves on this machine, not in a box"; exit 1; }
	@test -n "$(HI_FOLD)" || { echo "hi agent best-of runs make fold between folds, with HI_FOLD set"; exit 1; }
	@UV_PROJECT_ENVIRONMENT=$(PROJECT)/.venv HOLDOUT=$(HOLDOUT) VALIDATION=$(VALIDATION_DIR)/validation.txt \
	  $(RUN) python prepare.py --folds $(FOLDS) --fold $(HI_FOLD)

loop: ## Rounds in the background: BOXES a round, up to ROUNDS, BUDGET dollars
	@test "$(FOLDS)" = 1 || grep -qs '"folds": $(FOLDS),' eval/split.json || \
	  { echo "eval/ wasn't written for $(FOLDS) folds; make data FOLDS=$(FOLDS), then commit eval/"; exit 1; }
	hi agent best-of $(BOXES) --rounds $(ROUNDS) --budget $(BUDGET) --patience 10 --max 30m \
	  --score "make score" --lower --edit train.py \
	  --validate "make validate" --validate-data $(VALIDATION_DIR) --validate-slack $(VALIDATE_SLACK) \
	  $(if $(filter-out 1,$(FOLDS)),--folds $(FOLDS) --fold "make fold FOLDS=$(FOLDS)" $(if $(FOLD_EVERY),--fold-every $(FOLD_EVERY))) \
	  program.md
