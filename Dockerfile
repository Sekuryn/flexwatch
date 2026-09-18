# syntax=docker/dockerfile:1.7
#
# Build multi-stage : le toolchain Go (~800 Mo) ne part jamais en production.
# L'image finale ne contient que le binaire statique + les certificats racine.
#
# Pourquoi distroless/static et pas scratch : on a besoin des CA racine pour
# joindre l'API Communauto et Telegram en TLS, et distroless fournit en plus un
# utilisateur non-root (65532) et /etc/passwd. Zéro shell, zéro package
# manager : rien à exploiter après une RCE, et rien à patcher tous les mardis.
#
# Pourquoi épingler par digest : un tag est mutable, un digest non. Sans ça, le
# build n'est pas reproductible et un tag repoussé change silencieusement la
# base. Remplace les digests ci-dessous par ceux que tu as vérifiés :
#   docker buildx imagetools inspect golang:1.27-alpine
#   docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot

ARG GO_IMAGE=golang:1.27-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# ---------------------------------------------------------------- build stage
FROM ${GO_IMAGE} AS build

WORKDIR /src

# Les dépendances d'abord : ce layer est mis en cache tant que go.mod/go.sum
# ne changent pas. (Le build par défaut n'a aucune dépendance tierce, donc
# go.sum peut être absent : le COPY reste tolérant via le glob.)
COPY go.mod go.su[m] ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=arm64

# CGO_ENABLED=0    : binaire statique, aucune libc dans l'image finale.
# -trimpath        : pas de chemins de build absolus dans le binaire (reproductibilité).
# -ldflags -s -w   : pas de table de symboles ni de DWARF (image plus petite).
# -buildvcs=true   : le commit git est intégré, visible via `flexwatch -version`.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -buildvcs=true \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/flexwatch \
        ./cmd/flexwatch

# Les tests tournent dans la CI, pas dans l'image : un build d'image ne doit
# pas être le seul endroit où la qualité est vérifiée.

# -------------------------------------------------------------- runtime stage
FROM ${RUNTIME_IMAGE}

ARG VERSION=dev
LABEL org.opencontainers.image.title="flexwatch" \
      org.opencontainers.image.description="Detecteur de vehicules Communauto Flex (lecture seule, notify-first)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/Fougere/flexwatch" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/flexwatch /flexwatch

# 65532 = nonroot chez distroless. Aucun besoin de root : pas de port < 1024,
# pas d'écriture disque.
USER 65532:65532

EXPOSE 2112

# Pas de HEALTHCHECK : l'image n'a pas de shell ni de curl (c'est voulu). La
# supervision passe par /healthz et /readyz, sondés par Kubernetes ou l'ALB.
ENTRYPOINT ["/flexwatch"]
