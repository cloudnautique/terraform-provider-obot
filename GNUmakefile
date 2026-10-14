.DEFAULT_GOAL := build

OBOT_REF ?= main
OBOT_INSTANCE ?= dev
OBOT_INSTANCES ?= 1
TESTARGS ?=
ACCEPTANCE_TEST = go test -count=1 -timeout=60m -run=TestAcc $(TESTARGS) ./internal/provider/...

# Discobox shares the default Go cache volume across sandboxes. Keep this
# checkout's caches on its own disk, and honor explicit caller overrides.
ifneq ($(wildcard /etc/discobox/sandbox.json),)
export GOCACHE ?= $(CURDIR)/.obot/cache/go-build
export GOMODCACHE ?= $(CURDIR)/.obot/cache/go-mod
export GOLANGCI_LINT_CACHE ?= $(CURDIR)/.obot/cache/golangci-lint
endif

help:
	@printf '%s\n' \
		'make test                       Run Go and Python unit tests; no Obot needed' \
		'make test-acceptance            Run against existing Obot instance dev' \
		'  OBOT_INSTANCE=NAME            Select another existing instance' \
		'make test-acceptance-fresh      Create test instances, test, then remove them' \
		'  OBOT_REF=main|v0.26.3         Select the server and CLI version' \
		'  OBOT_INSTANCES=2              Include tests across multiple instances' \
		'make test-smoke                 Create instances, check auth, then remove them' \
		'make test-acceptance-env        Use OBOT_BASE_URL, OBOT_TOKEN, OBOT_TEST_EMAIL' \
		'  TESTARGS="-run=TestAccNAME"    Select acceptance tests' \
		'make obot-up / obot-down        Start / remove named instance (default: dev)' \
		'make build / lint / generate    Build, lint, or regenerate provider docs'

build:
	mkdir -p bin
	go build -o bin/terraform-provider-obot .

install:
	go install .

lint:
	golangci-lint run

generate:
	cd tools && go generate ./...

fmt:
	gofmt -s -w .

test: test-go test-tooling

test-go:
	TF_ACC= go test -cover -timeout=120s ./...

test-acceptance:
	python3 scripts/obot.py exec "$(OBOT_INSTANCE)" -- env TF_ACC=1 $(ACCEPTANCE_TEST)

test-acceptance-env:
	TF_ACC=1 $(ACCEPTANCE_TEST)

test-acceptance-fresh:
	python3 scripts/obot.py testacc --ref "$(OBOT_REF)" --instances "$(OBOT_INSTANCES)" -- $(ACCEPTANCE_TEST)

test-smoke:
	python3 scripts/obot.py smoke --ref "$(OBOT_REF)" --instances "$(OBOT_INSTANCES)"

test-tooling:
	python3 -m unittest discover -s scripts -p 'test_*.py'

obot-source:
	python3 scripts/obot.py source --ref "$(OBOT_REF)"

obot-cli:
	python3 scripts/obot.py cli --ref "$(OBOT_REF)"

obot-up:
	python3 scripts/obot.py up "$(OBOT_INSTANCE)" --ref "$(OBOT_REF)"

obot-down:
	python3 scripts/obot.py down "$(OBOT_INSTANCE)"

# Compatibility aliases for the original target names.
testacc: test-acceptance-env
testacc-local: test-acceptance-fresh
obot-smoke: test-smoke

# Keep development overrides local to this checkout.
dev: build
	@printf '%s\n' \
		'provider_installation {' \
		'  dev_overrides {' \
		'    "registry.terraform.io/obot-platform/obot" = "$(CURDIR)/bin"' \
		'  }' \
		'  direct {}' \
		'}' > dev.tfrc
	@echo 'Run: export TF_CLI_CONFIG_FILE="$(CURDIR)/dev.tfrc"'

dev-check: dev
	TF_CLI_CONFIG_FILE="$(CURDIR)/dev.tfrc" terraform -chdir=examples/dev providers schema -json > /dev/null
	TF_CLI_CONFIG_FILE="$(CURDIR)/dev.tfrc" terraform -chdir=examples/dev plan -input=false

.PHONY: help build install lint generate fmt test test-go test-tooling \
	test-acceptance test-acceptance-env test-acceptance-fresh test-smoke \
	testacc testacc-local obot-smoke dev dev-check obot-source obot-cli obot-up obot-down
