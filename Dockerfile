# syntax=docker/dockerfile:1

# --- build stage: pure-Go static binary (modernc sqlite → no CGO) ---
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cronova ./cmd/cronova

# --- runtime stage: minimal distroless image ---
FROM gcr.io/distroless/static-debian12:latest
WORKDIR /app
COPY --from=build /out/cronova /usr/local/bin/cronova
# container-friendly defaults (override via flags / CRONOVA_* env / compose)
ENV CRONOVA_DB=/app/data/cronova.db \
    CRONOVA_DAGS=/app/dags \
    CRONOVA_LOGS=/app/logs \
    CRONOVA_HTTP=:8090
EXPOSE 8090
# self-contained probe (distroless has no shell/curl): the binary hits /readyz
HEALTHCHECK --interval=15s --timeout=4s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/cronova", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/cronova"]
CMD ["serve"]
