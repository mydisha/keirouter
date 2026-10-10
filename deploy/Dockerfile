# syntax=docker/dockerfile:1

# ---- frontend build stage ----
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ .
RUN npm run build

# ---- backend build stage ----
FROM golang:1.26-alpine AS backend
WORKDIR /src/backend
# VERSION is injected into the binary via -ldflags so the dashboard reports a
# real version instead of the "dev" default. The .git dir is excluded from the
# build context, so git describe cannot run here — the value must come in as a
# build arg. Resolution order:
#   1. VERSION        — explicit (e.g. compose passes a release tag like v0.1.6)
#   2. SOURCE_COMMIT  — auto-injected by Coolify with the deployed git SHA
#   3. "dev"          — local/unstamped fallback
ARG VERSION=dev
ARG SOURCE_COMMIT=
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    VER="${VERSION}"; \
    if [ -z "${VER}" ] || [ "${VER}" = "dev" ]; then \
      if [ -n "${SOURCE_COMMIT}" ]; then VER="${SOURCE_COMMIT}"; else VER="dev"; fi; \
    fi; \
    echo "Building keirouter version: ${VER}"; \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.Version=${VER}" \
    -o /out/keirouter ./cmd/keirouter
RUN mkdir -p /out/data

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

ENV KEIROUTER_DATA__DIR=/data
ENV KEIROUTER_SERVER__HOST=0.0.0.0
ENV KEIROUTER_SERVER__PORT=20180
ENV KEIROUTER_SECURITY__BIND_LOOPBACK_ONLY=false

COPY --from=backend /out/keirouter /usr/local/bin/keirouter
COPY --from=backend --chown=nonroot:nonroot /out/data /data
COPY --from=frontend /src/frontend/dist /usr/local/share/keirouter/frontend/dist

USER nonroot:nonroot
EXPOSE 20180
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/usr/local/bin/keirouter", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/keirouter"]
