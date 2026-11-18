# Kicker

A digitized table soccer table: automatic goal counting, player identification via NFC coins and phones,
Elo ratings and statistics, tournaments, side displays with animations, and a companion app.
A Raspberry Pi runs the server; small USB modules (goal sensors, coin acceptors, corners, ball dispenser,
displays) plug into it, Home Assistant style.

The design is written down in the specs (`docs/`, maintained in the "Kicker" project):

| Spec | Content |
|---|---|
| 00 | Architecture decisions |
| 01 | Module protocol + capability catalog |
| 02 | Game engine |
| 03 | Auth, pairing, backups, onboarding |
| 04 | Repository layout, tooling, milestones |

## Layout
```
server/     Go server (one binary: serve, monitor, admin, loadtest)
web/        SvelteKit web UI, embedded into the server and reused by the app
app/        Tauri v2 companion app (from milestone M4)
firmware/   KickerModule Arduino library + module firmware (PlatformIO)
catalog/    generated JSON Schemas of the module capabilities
hardware/   module CAD, wiring, bench rig
deploy/     Raspberry Pi install script + systemd unit
docs/       specs and guides
```

## Getting started
Requirements: Go 1.25+, Node 22 + pnpm 10, PlatformIO (for firmware), make.

```sh
cd server && go mod tidy && cd ..   # first time only: resolves Go dependencies, creates go.sum
make dev                            # server with virtual modules on :8080 + web UI with hot reload on :5173
make test
make build-pi                       # bin/kicker-linux-arm64 for the Raspberry Pi 4
```

On the Pi (Raspberry Pi OS Lite 64-bit), once: `sudo deploy/install.sh kicker-linux-arm64`.
After that: `make deploy PI=pi@kicker.local`.

## Status
Milestone **M1 – "First goal"** (goal sensor → live score on the web), see spec 04 §7.
