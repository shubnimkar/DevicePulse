FROM golang:1.25-bookworm AS builder

WORKDIR /src/api

COPY api/go.mod api/go.sum ./
RUN go mod download

COPY api/. .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/devicepulse-api .

FROM golang:1.25-bookworm AS runtime

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates bash dpkg-dev \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
COPY --from=builder /out/devicepulse-api /usr/local/bin/devicepulse-api
COPY agent ./agent
COPY packaging ./packaging

ENV PORT=8000
ENV AGENT_BUILD_ROOT=/workspace
EXPOSE 8000

CMD ["devicepulse-api"]
