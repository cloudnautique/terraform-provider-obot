.DEFAULT_GOAL := build

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
	TF_ACC=1 go test -v -cover -timeout=120m ./...

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

.PHONY: build install lint generate fmt test testacc dev dev-check
