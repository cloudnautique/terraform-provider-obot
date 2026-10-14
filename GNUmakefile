.DEFAULT_GOAL := build

OBOT_REF ?= main
OBOT_INSTANCE ?= dev
OBOT_INSTANCES ?= 1

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

test:
	go test -v -cover -timeout=120s ./...

testacc:
	TF_ACC=1 go test -count=1 -timeout=60m -run=TestAcc ./internal/provider/...

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

obot-smoke:
	python3 scripts/obot.py smoke --ref "$(OBOT_REF)" --instances "$(OBOT_INSTANCES)"

testacc-local:
	python3 scripts/obot.py testacc --ref "$(OBOT_REF)" --instances "$(OBOT_INSTANCES)"

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

.PHONY: build install lint generate fmt test testacc dev dev-check test-tooling obot-source obot-cli obot-up obot-down obot-smoke testacc-local
