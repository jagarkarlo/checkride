PYTHON ?= python3
K3D ?= k3d

.PHONY: install test lint format lab-up lab-down

install:
	$(PYTHON) -m pip install -e ".[dev]"

test:
	$(PYTHON) -m pytest

lint:
	ruff check .
	ruff format --check .

format:
	ruff check --fix .
	ruff format .

lab-up:
	$(K3D) cluster create --config lab/k3d/source.yaml
	$(K3D) cluster create --config lab/k3d/restore.yaml

lab-down:
	$(K3D) cluster delete checkride-source checkride-restore
