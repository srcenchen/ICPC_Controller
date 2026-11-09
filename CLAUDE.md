# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ICPC 无锡学院 Linux 集控系统 — a centralized remote control system for managing Linux contestant machines in ICPC (competitive programming) competitions.

- **Server**: Deployed on Linux, optionally publishes via Avahi mDNS (`icpc-server.local`, disabled by default; enable with `--avahi=true`), provides a web UI
- **Client (选手机)**: Contestant machines running as root, connect to server, report system info, accept remote commands

## Tech Stack

- Go 1.26 backend
- SQLite3 (pure Go, no CGo — likely `modernc.org/sqlite` or similar)
- jQuery frontend embedded in the Go server binary (single-binary deployment)

## Directory Structure (planned)

```
cmd/
  server.go    — server entrypoint
  client.go    — client (contestant machine) entrypoint
internal/
  biz/         — business logic layer
  data/        — data access / repository layer
  model/       — data models / structs
  server/      — HTTP server, API handlers, embedded frontend
  service/     — service layer (orchestration between biz/data)
```

Standard Go project layout following clean architecture conventions.

## Commands

```bash
# Build
go build ./...

# Run server
go run ./cmd/server.go

# Run client
go run ./cmd/client.go

# Run tests
go test ./...

# Run a single test
go test -run TestName ./internal/...
```

## Key Requirements (from 需求.md)

### Server
1. When started with `--avahi=true --bind <local-ip>`, publish via `avahi-publish -a -R icpc-server.local <local-ip>` for mDNS discovery (default is off, and `--bind` is required when enabled)
2. Maintain persistent long connections to all client machines with automatic reconnection
3. Assign each client an incremental numeric ID (starting from 1); client renames its hostname to this ID
4. Store client machine info (hardware specs, OS details, etc.) in SQLite3
5. Web UI with jQuery providing:
   - **Dashboard** — current device status overview
   - **Device Management** — device list with summaries, click for full details
   - **Command Execution** — send commands to clients, with syntax-highlighted input

### Client (选手机)
1. Runs as **root**
2. Discovers server via `icpc-server.local` (mDNS), with fallback: reads `~/server` file for a direct IP if the file is non-empty
3. On first connection, requests an ID from the server, then renames its hostname to that ID
4. Collects system specs via `fastfetch --format json > /tmp/spec.json` and reports to server
5. Maintains a persistent long connection with reconnection logic
6. Accepts and executes commands from the server (file-based or direct commands)

### fastfetch JSON Output

The `fastfetch --format json` output is a JSON array of typed objects (`Title`, `OS`, `Host`, `Kernel`, `CPU`, `GPU`, `Memory`, `Disk`, `LocalIp`, `DE`, `WM`, etc.). The model layer should define structs to parse relevant fields from this output. See `需求.md` lines 16-363 for the full example schema.

### Client ID Assignment

- IDs are monotonically incrementing integers starting from 1
- When a client connects, the server assigns the next available ID
- The client then renames its hostname to `cwxu-icpc-{ID}` (e.g., `cwxu-icpc-1`, `cwxu-icpc-2`, ...)

## Data Model Highlights

Key entities the system tracks per client machine:
- ID (assigned by server)
- Hostname, username
- OS info (name, version, kernel)
- Hardware (CPU model/cores, GPU, total memory, disk layout)
- Network (IP addresses, interfaces)
- Connection status (online/offline)
- Shell, DE/WM info
