# Makefile flexwatch — cible Linux/WSL et la CI.
# Sous Windows/PowerShell sans make : utiliser scripts/make.ps1 (mêmes cibles).

SHELL := /bin/sh

BINARY      := flexwatch
PKG         := ./cmd/flexwatch
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE       ?= flexwatch
IMAGE_TAG   ?= $(VERSION)
GOFLAGS     := -trimpath
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Versions des outils épinglées : un lint qui change de version tout seul
# transforme la CI en loterie.
GOLANGCI_VERSION    := v2.13.0
STATICCHECK_VERSION := 2026.2.1
PGX_VERSION         := v5.7.2

.DEFAULT_GOAL := help

## help: liste les cibles disponibles
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## //' | awk -F': ' '{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## fmt: formate le code (à lancer avant de committer)
.PHONY: fmt
fmt:
	gofmt -s -w .

## fmt-check: échoue si du code n'est pas formaté (cible CI)
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Fichiers non formates :"; echo "$$unformatted"; \
		echo "Corriger avec : make fmt"; exit 1; \
	fi

## vet: analyse statique du toolchain Go
.PHONY: vet
vet:
	go vet ./...

## lint: golangci-lint (doit être installé, cf. make tools)
.PHONY: lint
lint:
	golangci-lint run ./...

## staticcheck: analyse staticcheck seule
.PHONY: staticcheck
staticcheck:
	staticcheck ./...

## vuln: govulncheck sur le code et les dépendances
.PHONY: vuln
vuln:
	govulncheck ./...

## test: tests unitaires avec race detector
.PHONY: test
test:
	go test -race -count=1 ./...

## cover: tests + rapport de couverture
.PHONY: cover
cover:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./cmd/... ./internal/...
	go tool cover -func=coverage.out | tail -n 1

## e2e: test de bout en bout local contre un faux serveur (~60 s)
.PHONY: e2e
e2e:
	bash test/e2e/run.sh

## check: tout ce que la CI vérifie, en local
.PHONY: check
check: fmt-check vet lint staticcheck vuln test

## build: binaire pour la plateforme courante
.PHONY: build
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

## build-linux-arm64: binaire pour EC2 t4g (Graviton)
.PHONY: build-linux-arm64
build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 $(PKG)

## build-postgres: binaire avec la persistance Postgres (ajoute la dépendance pgx)
.PHONY: build-postgres
build-postgres:
	go get github.com/jackc/pgx/v5@$(PGX_VERSION)
	go mod tidy
	CGO_ENABLED=0 go build $(GOFLAGS) -tags postgres -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-postgres $(PKG)

## run: lance le bot en local (config par défaut : centre-ville de Montréal)
.PHONY: run
run:
	go run $(PKG)

## once: un seul poll, affiche ce que l'API renvoie et sort
.PHONY: once
once:
	go run $(PKG) -once

## probe: appelle l'API brute (utile pour vérifier le schéma à la main)
.PHONY: probe
probe:
	curl -sS -H 'Accept: application/json' \
		'https://restapifrontoffice.reservauto.net/api/v2/Vehicle/FreeFloatingAvailability?CityId=59&MaxLatitude=45.55&MinLatitude=45.45&MaxLongitude=-73.50&MinLongitude=-73.65' \
		| head -c 2000

## docker: construit l'image (arm64 par défaut, comme la cible EC2 t4g)
.PHONY: docker
docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg TARGETARCH=arm64 -t $(IMAGE):$(IMAGE_TAG) .

## sbom: génère le SBOM CycloneDX de l'image avec syft
.PHONY: sbom
sbom:
	syft $(IMAGE):$(IMAGE_TAG) -o cyclonedx-json=sbom.json
	@echo "SBOM ecrit dans sbom.json"

## scan: scanne l'image avec Trivy (échoue sur HIGH/CRITICAL)
.PHONY: scan
scan:
	trivy image --severity HIGH,CRITICAL --exit-code 1 --ignore-unfixed $(IMAGE):$(IMAGE_TAG)

## tools: installe les outils de dev dans $(go env GOPATH)/bin
.PHONY: tools
tools:
	# golangci-lint v2 : le module a change de chemin (/v2). L'installation
	# officielle recommandee est le binaire, mais go install reste pratique.
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@latest

## lint-postgres: linte la variante postgres (ajoute la dependance pgx)
.PHONY: lint-postgres
lint-postgres:
	go get github.com/jackc/pgx/v5@$(PGX_VERSION)
	golangci-lint run --build-tags postgres ./...
	@echo "Penser a restaurer go.mod si l'ajout de pgx n'etait pas voulu : git checkout go.mod go.sum"

## clean: supprime les artefacts de build
.PHONY: clean
clean:
	rm -rf bin coverage.out sbom.json trivy-report.json
