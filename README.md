# gochat

[![ci](https://github.com/Satyam-1o/gochat/actions/workflows/ci.yml/badge.svg)](https://github.com/Satyam-1o/gochat/actions/workflows/ci.yml)
[![release](https://github.com/Satyam-1o/gochat/actions/workflows/release.yml/badge.svg)](https://github.com/Satyam-1o/gochat/actions/workflows/release.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A peer-to-peer, terminal-based group chat. Talk in encrypted, passphrase-protected
rooms with a handful of friends over the internet — no accounts, no servers holding
your messages.

- **Terminal UI** built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).
- **P2P transport** via [libp2p](https://libp2p.io/) (TCP + QUIC, hole punching,
  circuit relay).
- **Group rooms**: many peers per room, one gossipsub topic per room.
- **Works over the internet** through a tiny rendezvous/relay node you host yourself.
- **Rooms persist, messages don't**: the app remembers which rooms you joined and
  auto-reconnects on launch, but chat history is ephemeral and disappears on exit.
- **Private by construction**: a room's network topic is derived from the
  passphrase (Argon2id → SHA-256), and every message is encrypted with
  XChaCha20-Poly1305. The rendezvous node only ever sees hashed topics and
  ciphertext.

---

## How it works

```
        alice                     rendezvous node                    bob
  ┌──────────────┐            ┌────────────────────┐          ┌──────────────┐
  │  Bubble Tea  │            │   PEX server       │          │  Bubble Tea  │
  │  room mgr    │  register  │   circuit relay    │ register │  room mgr    │
  │  gossipsub ──┼───────────▶│   pubsub bridge ───┼─────────▶│── gossipsub  │
  └──────────────┘            └────────────────────┘          └──────────────┘
        ▲                                                             │
        └──────────────── encrypted messages relayed ────────────────┘
```

1. Every client connects to your **rendezvous node** (a multiaddr you configure once).
2. When you join a room, the client subscribes to the room's gossipsub topic and
   registers that (hashed) topic with the node over a small PEX protocol.
3. The node introduces peers in the same topic and also acts as a **pubsub bridge**:
   it subscribes to the topic and relays encrypted messages between clients. This
   means chat works even when neither client can dial the other directly (the common
   case behind NAT). Direct peer-to-peer links, when they do form, are a performance
   optimization on top.
4. On a LAN, clients also find each other via mDNS without any server at all.

The rendezvous node never sees room names or message contents — only hashed topics,
peer IDs, and ciphertext. You can host it on the smallest VPS you can find.

---

## Requirements

- Go 1.27+ to build (or use the provided Docker image for the server).
- Any modern terminal.
- For internet use: a small always-on machine with a public IP (a VPS, or the
  [Terraform setup](deploy/terraform/README.md) on AWS).
- Terraform 1.5+ only if you use the AWS option.

---

## Build

```sh
git clone https://github.com/Satyam-1o/gochat.git
cd gochat
make build          # -> bin/gochat and bin/gochat-rendezvous
```

Or install with the Go toolchain:

```sh
go install github.com/Satyam-1o/gochat/cmd/gochat@latest
go install github.com/Satyam-1o/gochat/cmd/gochat-rendezvous@latest
```

Prebuilt binaries for Linux, macOS and Windows (amd64/arm64) are attached to each
[release](https://github.com/Satyam-1o/gochat/releases). Either way, `gochat -version`
and `gochat-rendezvous -version` print the build version, commit and date.

Or build directly:

```sh
go build -o bin/gochat ./cmd/gochat
go build -o bin/gochat-rendezvous ./cmd/gochat-rendezvous
```

---

## 1. Run a rendezvous node

Pick **one** of the following. Whatever you choose, the node prints a
ready-to-paste `client: gochat -bootstrap "..."` address.

### Option A — one-line installer (recommended, any Linux VPS)

Installs the latest release as a hardened systemd service, creates a service
user, opens the listen port in the local firewall, and prints the bootstrap
address. Works on Debian/Ubuntu, RHEL/Fedora, and Amazon Linux.

```sh
curl -fsSL -o install-rendezvous.sh \
  https://raw.githubusercontent.com/Satyam-1o/gochat/main/scripts/install-rendezvous.sh
sudo bash install-rendezvous.sh
```

Run `sudo bash install-rendezvous.sh --help` for options such as `--port`,
`--public`, and `--version`.

### Option B — AWS EC2 (Terraform)

Provisions an instance, security group, Elastic IP, and IAM-for-SSM, then runs
the installer for you:

```sh
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars
terraform init && terraform apply
```

See [`deploy/terraform/README.md`](deploy/terraform/README.md) for prerequisites,
cost, and how to read the bootstrap address from the node.

### Option C — systemd (manual)

```sh
make rendezvous
sudo install -m 0755 bin/gochat-rendezvous /usr/local/bin/gochat-rendezvous
sudo useradd --system --home /var/lib/gochat --create-home --shell /usr/sbin/nologin gochat
# Edit deploy/gochat-rendezvous.service and set -public to your server's IP/DNS:
sudo install -m 0644 deploy/gochat-rendezvous.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gochat-rendezvous
sudo journalctl -u gochat-rendezvous -n 40 --no-pager
```

Open the port (both TCP and UDP):

```sh
sudo ufw allow 4001/tcp && sudo ufw allow 4001/udp
```

### Option D — Docker

```sh
cd deploy
docker compose up -d
docker compose logs -f          # look for the "client: gochat -bootstrap ..." line
```

### Option E — run it directly

```sh
./bin/gochat-rendezvous -port 4001 -data ./rendezvous-data -public 203.0.113.5
```

The node prints:

```
  peer ID : 12D3KooW...
  client-ready address (public host):
    /ip4/203.0.113.5/tcp/4001/p2p/12D3KooW...
    client: gochat -bootstrap "/ip4/203.0.113.5/tcp/4001/p2p/12D3KooW..."
```

Copy that **`gochat -bootstrap ...`** line — you'll give it to clients in the next step.

> If your server is behind NAT or you use a DNS name, pass `-public <host>` or one or
> more `-announce /dns4/chat.example.com/tcp/4001/p2p/<peerID>` flags. Run
> `gochat-rendezvous -h` for all options.

---

## 2. Use the client

```sh
./bin/gochat -bootstrap "/ip4/203.0.113.5/tcp/4001/p2p/12D3KooW..."
```

The bootstrap address is saved, so later launches just need:

```sh
./bin/gochat
```

Then, inside the TUI:

```
/join secret-room correct-horse-battery-staple
```

Everyone who wants to talk runs the same command with the **same room name and
passphrase**. That's it — messages are end-to-end encrypted and lost when you quit.

Use `-nick <name>` to set a display name, or `/nick <name>` inside the app.

### Commands

| Command | Description |
| --- | --- |
| `/join <room> <passphrase>` | Join (or create) a private room |
| `/leave` | Leave the active room |
| `/nick <name>` | Change your display name |
| `/rooms` | List saved rooms and peer counts |
| `/peers` | List peers in the active room |
| `/clear` | Clear the message pane |
| `/help` | Show help |
| `/quit` | Exit |

### Keys

| Key | Action |
| --- | --- |
| `Enter` | Send the message |
| `Tab` / `Esc` | Switch focus between sidebar and input |
| `↑` / `↓` | Select a room in the sidebar |
| `PgUp` / `PgDn` | Scroll the message pane |
| `Ctrl+C` | Quit |

---

## Data & config locations

| What | Where |
| --- | --- |
| Client data | `$XDG_CONFIG_HOME/gochat` (usually `~/.config/gochat`) unless `-config` is set |
| Identity key | `<config>/identity` (0600) |
| Saved rooms | `<config>/rooms.json` (0600, stores the derived room key + topic, **not** the passphrase) |
| Display name / bootstrap | `<config>/config.json` |
| Client log | `<config>/gochat.log` (0600; library logs go here so they can't corrupt the TUI — use `-debug` to log to stderr instead) |

The rendezvous node stores only its identity key (in `-data`; the installer uses
`/var/lib/gochat`), which keeps its peer ID — and therefore your clients' saved
bootstrap address — stable across restarts.

---

## Headless mode (scripts & CI)

The client can run without the TUI, which is handy for verifying a deployment:

```sh
# Receiver: wait up to 45s for a message containing "ping"
./bin/gochat -headless -config /tmp/bob -nick bob \
  -bootstrap "$ADDR" -room demo -pass hunter2 -expect ping -timeout 45s

# Sender: send "ping" every 2s
./bin/gochat -headless -config /tmp/alice -nick alice \
  -bootstrap "$ADDR" -room demo -pass hunter2 -send ping -timeout 40s
```

Passphrases can also be read from a file with `-pass-file` (preferred, since `-pass`
is visible in the process list).

---

## Testing

```sh
make test     # full unit + integration suite
make check    # gofmt check + go vet + tests
make smoke    # build and run an end-to-end test with the real binaries
```

The suite includes an end-to-end test (`TestEndToEndViaRendezvous`) that stands up a
rendezvous node plus two clients which discover each other only through it and
exchange an encrypted message. `scripts/smoke-test.sh` does the same with the built
binaries.

---

## Releasing

Releases are built by CI with [GoReleaser](https://goreleaser.com). To cut one:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow builds Linux/macOS/Windows (amd64/arm64) archives — each
containing both binaries — plus `checksums.txt`, and attaches them to the GitHub
Release. To preview the artifacts locally without publishing:

```sh
goreleaser release --snapshot --clean   # -> ./dist
```

---

## Troubleshooting

**Clients don't find each other over the internet.**
- Make sure every client was started with the same `-bootstrap` address and joined
  the same room **name and passphrase**.
- Check the node is reachable: `nc -vz <host> 4001` (TCP) — remember to open UDP too.
- Verify the peer ID in the bootstrap address matches the node's startup banner.

**It works on a LAN but not across the internet.**
- You likely have no rendezvous node configured. Set `-bootstrap ...` (this is saved).
- The rendezvous node's `-public` host must be reachable from the clients.

**"wrong passphrase"-style behavior: two people see the room but no messages.**
- Room topics are hash-derived from the passphrase; a different passphrase means a
  different, invisible room. Everyone must use the exact same passphrase.

**Direct peer connections fail in the logs.**
- That's expected and harmless. Delivery goes through the rendezvous bridge; direct
  links are only an optimization.

**Need to see what the client is doing?**
- The TUI writes library logs to `<config>/gochat.log`. Re-run with `-debug` to send
  them to stderr instead (useful when piping output).

---

## Project layout

```
cmd/gochat/              client entry point (TUI + headless)
cmd/gochat-rendezvous/   rendezvous/relay node entry point
internal/crypto/         Argon2id key derivation, topic hashing, XChaCha20-Poly1305
internal/identity/       persistent Ed25519 libp2p identity
internal/node/           libp2p host, mDNS, PEX wiring, pubsub bridge
internal/pex/            peer-exchange protocol (register/discover/introduce)
internal/room/           gossipsub room manager, encryption, presence, persistence
internal/store/          JSON persistence (config, rooms), atomic writes, 0600
internal/tui/            Bubble Tea model, view and commands
internal/version/        build metadata injected via -ldflags
deploy/                  systemd unit, Dockerfile, docker-compose
deploy/terraform/        AWS EC2 deployment (Terraform)
scripts/install-rendezvous.sh  one-line server installer
scripts/smoke-test.sh    end-to-end test with the real binaries
.github/workflows/       CI (test + cross-build) and tag-driven release
.goreleaser.yaml         release builds (binaries + checksums)
```

---

## Security & privacy notes

- **Passphrase → key**: Argon2id derives a per-room key; a SHA-256-based topic is
  published instead of the room name.
- **Message encryption**: XChaCha20-Poly1305 with a per-room message key
  (HKDF-derived); the AEAD's associated data binds the sender's peer ID and the topic,
  so ciphertext can't be replayed into another room or attributed to another peer.
- **What the rendezvous node can see**: hashed room topics, peer IDs/addresses, message
  timing and sizes. It cannot read your messages or learn your room names.
- **What the rendezvous node cannot do**: decrypt messages, enumerate passphrases
  efficiently (Argon2id), or join a room without the passphrase.
- Messages are **not persisted** and there is no offline history.

---

## License

[MIT](LICENSE) © 2026 Satyam-1o.
