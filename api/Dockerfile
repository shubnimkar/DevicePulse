FROM golang:1.25-bookworm AS builder

WORKDIR /src/api

COPY api/go.mod api/go.sum ./
RUN go mod download

COPY api/. .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/devicepulse-api .

FROM golang:1.25-bookworm AS runtime

# bash + dpkg-dev support in-container agent builds (POST /update/builds).
# fakeroot is unnecessary: packaging/linux/build_deb.sh passes
# dpkg-deb --root-owner-group, so non-root builds still produce root-owned debs.
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates bash dpkg-dev \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --create-home --uid 10001 appuser

WORKDIR /workspace
COPY --from=builder /out/devicepulse-api /usr/local/bin/devicepulse-api
COPY --chown=appuser:appuser agent ./agent
COPY --chown=appuser:appuser packaging ./packaging

ENV PORT=8000 \
    AGENT_BUILD_ROOT=/workspace \
    HOME=/home/appuser \
    GOPATH=/home/appuser/go \
    GOCACHE=/tmp/devicepulse-go-build-cache
EXPOSE 8000

# Run unprivileged. Build artifacts land in $GOCACHE and os.MkdirTemp dirs,
# both of which are world-writable locations.
USER appuser
CMD ["devicepulse-api"]
