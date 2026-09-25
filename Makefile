PYTHON ?= python3
GO ?= go
K3D ?= k3d

.PHONY: install test lint format go-test go-vet go-build test-all lab-up lab-down

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

go-test:
	$(GO) test -race ./...

go-vet:
	$(GO) vet ./...
	test -z "$$($(GO)fmt -l cmd internal)"

go-build:
	$(GO) build ./...

test-all: test go-test

lab-up:
	$(K3D) cluster create --config lab/k3d/source.yaml
	$(K3D) cluster create --config lab/k3d/restore.yaml

lab-down:
	$(K3D) cluster delete checkride-source checkride-restore
