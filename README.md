# whatsappBot-golang

WhatsApp bot written in Go (port of the [whatsappBot-node](../whatsappBot-node) Node.js app), built with [whatsmeow](https://go.mau.fi/whatsmeow) — no Chromium, no Puppeteer, native multidevice protocol.

## Features

- **Newspaper kiosk**: `nacional`, `internacional`, `revista` list available files; `periodico: N`, `newspaper: N`, `magazine: N` reply with a one-time download link (RS256 JWT, max 3 uses, 5 min validity)
- **Subscriptions**: `subs: keyword` notifies you (48h link) when a matching file arrives; `del_subs`, `list_subs`
- **Weather**: `tiempo: city` — OpenWeatherMap data rewritten as a friendly note by GPT
- **TTS**: `audio: text` — replies with a Spanish voice note and deletes your command message
- **Image generation**: `imagen: prompt` — Stability AI
- **Electricity prices**: `electricidad` — PVPC chart from ESIOS (REE), with per-day cache and tomorrow's data after 19:00 Madrid time
- **AI fallback**: mention the bot in a group and GPT answers (text or voice); troll-listed users get trolled
- **Observability**: Prometheus metrics on all layers (HTTP, commands, repository, external APIs) + OTLP traces (Tempo) + `trace_id`-correlated logs

## Architecture

Package-by-layer with consumer-defined interfaces and manual DI (`app/`):

```
cmd/main.go        bootstrap
app/               App builder + wiring
config/            CLI flag configuration with validation
whatsapp/          whatsmeow adapter (Sender iface), command router (regex registry)
services/          kiosk, subscription, weather, tts, ai, imagegen, electricity, jwt
repository/        SQLite (modernc, no CGO), parameterized queries, instrumented decorator
http/              handlers (fileserver, trigger, health, version, metrics) + middleware
o11/               OTel setup, metrics factories, trace-aware slog handler
```

HTTP servers:
- **Public** (`-http-port`, default 46564): `GET /{nacional,internacional,magazine}_folder/{file}?access_token=...` — JWT + replay protection
- **Internal** (`-internal-port`, default 9000): `POST /api/v1/file` (subscription trigger), `GET /api/v1/health`, `GET /api/v1/version`, `GET /metrics`

## Build & run

```bash
make keys        # generate RSA keys for JWT (once)
make build       # vet + fmt + tidy + build -> build/main
make test        # go test -race ./...
```

Run (minimal flags):

```bash
./build/main \
  -bot-number 34XXXXXXXXX \
  -bot-notification-jid 34XXXXXXXXX@c.us \
  -nacional-folder /data/nacional/ \
  -internacional-folder /data/internacional/ \
  -magazine-folder /data/magazine/ \
  -db-path /data/db.sqlite \
  -session-db-path /data/session.db \
  -jwt-public-key-path keys/public.pem \
  -jwt-private-key-path keys/private.pem
```

First login shows a QR code in the terminal (or use `-bot-pair-phone 34XXXXXXXXX` for a pairing code). The session is stored in the session DB — subsequent runs connect directly.

### Key flags

| Flag | Default | Description |
|---|---|---|
| `-bot-number` | | Bot phone number (e.g. `34936674253`) |
| `-bot-mentioned-number` | | Bot `@lid` JID for group mentions |
| `-bot-notification-jid` | | JID that receives restart notifications |
| `-bot-troll-numbers` | | Comma-separated JIDs that get the troll AI |
| `-bot-pair-phone` | | Request pairing code instead of QR (first login) |
| `-{nacional,internacional,magazine}-folder` | `/tmp/periodico/...` | Kiosk folders |
| `-retention-{nacional,internacional,magazine}` | `24 / 169 / 720` | Retention (hours) |
| `-url-server` | `localhost:46564` | Public host:port for download links |
| `-http-port` / `-internal-port` | `46564 / 9000` | Server ports |
| `-db-path` / `-session-db-path` | `/tmp/db.sqlite`, `/tmp/session.db` | SQLite paths |
| `-openai-api-key` / `-openai-model` | / `gpt-4o-mini` | OpenAI |
| `-weather-api-key` | | OpenWeatherMap |
| `-stability-api-key` | | Stability AI |
| `-esios-api-key` | | ESIOS (REE) |
| `-o11-tracer-endpoint` | | OTLP gRPC endpoint (e.g. `tempo:4317`) |
| `-o11-prometheus-path` | `/metrics` | Metrics path on the internal server |
| `-o11-environment` | `dev` | Environment tag in telemetry |

Full list: `./build/main -h`

## Container

```bash
make container   # podman build -t whatsappbot-golang:dev
```

```bash
podman run -d --name whatsappbot \
  -p 46564:46564 -p 127.0.0.1:9000:9000 \
  -v ./nacional:/data/nacional:ro \
  -v ./internacional:/data/internacional:ro \
  -v ./magazine:/data/magazine:ro \
  -v ./data/db.sqlite:/data/db.sqlite \
  -v ./data/session.db:/data/session.db \
  -v ./keys:/app/keys:ro \
  whatsappbot-golang:latest \
  -jwt-public-key-path /app/keys/public.pem \
  -jwt-private-key-path /app/keys/private.pem \
  -bot-number 34XXXXXXXXX \
  -bot-notification-jid 34XXXXXXXXX@c.us \
  -nacional-folder /data/nacional/ \
  -internacional-folder /data/internacional/ \
  -magazine-folder /data/magazine/ \
  -db-path /data/db.sqlite \
  -session-db-path /data/session.db
```

## CI/CD

- **CI** (`.github/workflows/ci.yml`): vet, fmt, govulncheck, `go mod tidy` check, build, `go test -race`, container build verification
- **Release** (`.github/workflows/release.yml`): on tag `vX.Y.Z` — validates semver, runs tests, builds and pushes the container to `ghcr.io` with tags `vX.Y.Z`, `X.Y`, `X`, `latest`, and creates a GitHub release

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Observability

- **Metrics** (Prometheus, internal server): `http_server_requests_total`, `http_server_request_duration_seconds`, `whatsapp_commands_total{command,result}`, `whatsapp_command_duration_seconds`, `repo_queries_total{operation,result}`, `repo_query_duration_seconds`, `external_api_{calls_total,duration_seconds}{service,target,status}` plus business counters (`kiosk_links_generated_total`, `subscription_events_total`, `files_served_total`, `jwt_tokens_rejected_total`, `electricity_cache_total`, `ai_replies_total`, ...)
- **Traces**: OTLP gRPC to Tempo (`-o11-tracer-endpoint tempo:4317`), spans across HTTP → service → repository; exemplars link metrics to traces
- **Logs**: slog text logs with `trace_id` / `span_id` injected per request/command

## License

See [LICENSE](LICENSE).
