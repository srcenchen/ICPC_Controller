# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ICPC 无锡学院 Linux 集控系统 — a centralized remote control system for managing Linux contestant machines in ICPC (competitive programming) competitions.

- **Server (`./cmd/server`)**: Single-binary Go server with embedded web console. Supports **Standalone (单机)**, **Relay (并机/机房中转)**, and **Cloud (云端汇总)** deployment modes. Optionally publishes `icpc-server.local` via Avahi mDNS (`--avahi=true --bind <ip>`, disabled by default).
- **Client (`./cmd/client`, 选手机)**: Runs as root on Linux contestant machines, registers via hardware UUID + MAC fingerprint, reports hardware/health telemetry, executes commands, opens PTY remote terminals, serves X11 screen captures/watermarks, performs P2P file distribution with SHA-256 receipts, and provides local check-in + DOMjudge attendance endpoints (`127.0.0.1:8090`).

## Tech Stack

- **Backend**: Go 1.26 (`modernc.org/sqlite` pure-Go SQLite3, `gorilla/websocket`, `creack/pty`, `jezek/xgb`, `excelize/v2`)
- **P2P Engine**: `pkg/go-silver-core` (local module replaced in `go.mod`)
- **Frontend**: Vue 3 + TypeScript + Vite + Element Plus + `@xterm/xterm` + `highlight.js` (`frontend/`), built into `internal/server/web/` and embedded via `//go:embed web/*`

## Directory Structure

```text
cmd/
  server/main.go        — server entrypoint (--port 8080, --tcp-port 8081, --p2p-port 48080, --db icpc.db)
  client/               — Linux contestant client (main, screen, watermark, x11env, distribution, health, update, judge_bridge)
internal/
  biz/                  — hub connection registry, atomic ID/identity assignment, command dispatcher, janitor
  data/                 — SQLite schema/migrations, repositories, VACUUM INTO backup & restore
  model/                — protocol messages, device/fastfetch models, command/broadcast models
  server/               — HTTP mux, middleware, Avahi lifecycle, embedded web/ static assets
  service/              — REST/WS handlers, TCP client protocol, terminal hub, screen proxy, federation, P2P distribution, snapshots
frontend/
  src/
    lib/                — API client & room proxy resolver, reactive store, frame concurrency pool, search/format helpers
    components/         — DeviceDrawer, TerminalDialog, ScreenTile/ScreenDialog, TargetPicker, CodeEditor, FleetTable, JobsTable, SnapshotsCard
    pages/              — Login, Dashboard, Rooms, Devices, Screen, Commands, Distribute, Network, Power, Checkin, Broadcast, Settings
pkg/
  go-silver-core/       — chunked P2P file distribution library and protocol tests
```

## Build & Test Commands

```bash
# Build frontend into internal/server/web/ (always run after editing frontend/)
make ui

# Build server and client binaries
make build

# Cross-compile Linux amd64 client
make client-linux

# Run Go vet and all unit/integration tests
go vet ./...
go test -race ./...

# Run go-silver-core P2P & protocol tests
make test-p2p

# Run a single Go test
go test -race -run TestName ./internal/service
```

## Key Architectural Notes

- **Embedded Frontend**: Editing files in `frontend/` does NOT affect the Go binary until `make ui` (or `cd frontend && npm run build`) regenerates `internal/server/web/`. Note that `internal/server/web/broadcast/` holds the standalone contestant display pages (`before.html`, `contesting.html`, `after.html`) — keep `emptyOutDir: false` in `vite.config.ts` so broadcast display pages are preserved.
- **Screen Wall Concurrency**: Browsers cap HTTP/1.1 connections per origin at ~6. Grid tiles MUST fetch single JPEG snapshots (`?hd=0&single=1`) through `frontend/src/lib/framePool.ts` (never open unbounded `multipart/x-mixed-replace` streams in a grid). Only the single enlarged `ScreenDialog` opens a live MJPEG stream, and it explicitly resets `img.src` before unmounting so Chromium releases the socket immediately. On the server, `ScreenProxyHandler` coalesces concurrent thumbnail requests and caches frames briefly.
- **Interactive Terminal**: Each browser WebSocket connection to `/ws/terminal/{id}` gets a unique session ID (`term_<id>_<uuid>`) and is the sole writer on its `websocket.Conn` via `TerminalHub.SubscribeStream`. Keystrokes are sent as binary WebSocket frames; resize events (`{"type":"resize","cols":...,"rows":...}`) are sent as text frames so pasting JSON into the terminal is never mistaken for a control message.
- **Cloud Room Proxy**: When `store.roomID` is set in Cloud mode, `frontend/src/lib/api.ts` rewrites room-scoped endpoints to `/api/cluster/rooms/{room}/proxy/...` (validated by `allowedRelayRequest` in `internal/service/federation.go`).
- **Default Contestant Save Directory**: File distribution defaults `save_dir` to `/home/cwxu/桌面`.
