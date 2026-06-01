# Rendezvous

A lightweight, self-hostable signaling server for establishing direct P2P connections via UDP hole punching.

> **The server is only used to exchange UDP addresses between peers. Once the connection is established, all traffic flows directly between peers — the server is no longer involved.**

> ⚠️ **This project is a work in progress. The P2P connection is currently unencrypted. See the [roadmap](#roadmap) below.**

A public instance is available at `https://rendezvous.enzogp.dev`. You can also self-host your own instance.

---

## How it works

```
Peer A                  Rendezvous Server                 Peer B
  |                           |                              |
  |-- discovers public IP via STUN (Google)                 |
  |-- POST /session/abc ----->|                              |
  |   (waits...)              |<----- POST /session/abc -----|
  |                           |    (server crosses addresses)|
  |<-- peer B's address ------|------ peer A's address ----->|
  |                           |                              |
  |<============= UDP hole punching (direct) ===============>|
  |                           |                              |
  |<============= direct P2P connection ===================>|
```

1. Each peer discovers its public IP:port via STUN
2. Both peers register their UDP address on the rendezvous server using the same session ID
3. The server exchanges their addresses and both peers start hole punching simultaneously
4. Once the connection is established, the server is no longer needed

---

## Self-hosting

You can run your own rendezvous server. Pre-built binaries for the server are available on the [releases page](https://github.com/neozmmv/rendezvous/releases) — no Docker required.

### Option 1 — Binary + Cloudflare Tunnel (recommended)

No public IP or port forwarding required.

```bash
# download the server binary for your platform from the releases page
chmod +x rendezvous_linux_amd64
./rendezvous_linux_amd64

# in another terminal, expose it via Cloudflare Tunnel
cloudflared tunnel --url http://localhost:8000
```

Cloudflare will print a public URL — use that as your hostname in the client.

### Option 2 — Docker + Cloudflare Tunnel

### Requirements

- Docker + Docker Compose
- [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)

### docker-compose.yml

```yaml
services:
  rendezvous:
    build: .
    ports:
      - "8000:8000"
  cloudflare:
    image: cloudflare/cloudflared:latest
    command: tunnel --url http://rendezvous:8000
    depends_on:
      - rendezvous
```

```bash
docker compose up -d --build
```

Cloudflare will print a public URL in the logs — use that as your hostname.

---

## API

### `POST /session/:id`

Simple session without a password. The first peer to arrive waits; when the second peer registers, both receive each other's address simultaneously.

**Request:**
```json
{
  "udp_addr": "191.176.32.57:51740"
}
```

**Response (both peers):**
```json
{
  "peer": "201.x.x.x:55321"
}
```

---

### `POST /create_session`

Creates a password-protected session. Must be called before peers can join.

**Request:**
```json
{
  "id": "my-network",
  "password": "secret"
}
```

**Response:**
```json
{
  "message": "session created"
}
```

> Sessions expire automatically after 5 minutes if no peers join.

---

### `POST /join_session/:id`

Joins a password-protected session. Works the same as `/session/:id` but requires a password. The first peer waits; the second peer triggers the exchange.

**Request:**
```json
{
  "udp_addr": "191.176.32.57:51740",
  "password": "secret"
}
```

**Response (both peers):**
```json
{
  "peer": "201.x.x.x:55321"
}
```

**Error responses:**
```json
{ "error": "session not found" }
{ "error": "incorrect password" }
{ "error": "session already full" }
```

---

## Client

The client handles everything automatically: STUN discovery, signaling, hole punching, keepalive, and disconnect detection.

### Download

Pre-built binaries for Windows, Linux, and Linux ARM64 are available on the [releases page](https://github.com/neozmmv/rendezvous/releases). No installation required — just download and run.

### Usage

```
Enter hostname (blank for default): 
Create or join session? (c/j): j
Enter session: my-session
Does the session require a password? (y/n): n
Public addr: 191.176.32.57:51740
Listening on [::]:51740
Peer address: 201.x.x.x:55321
```

If you leave the hostname blank, it defaults to `https://rendezvous.enzogp.dev`.

### Session types

**Simple session (no password):**
- Both peers enter the same session ID
- No need to create it first — the first peer to arrive waits automatically

**Password-protected session:**
- One peer creates the session with `c` (create) and sets a password
- The other peer joins with `j` (join) and enters the same password
- Share the session ID and password out of band (e.g. via chat)

### Build from source

```bash
git clone https://github.com/neozmmv/rendezvous
cd rendezvous/client

# Linux
go build -o p2p_client .

# Windows
GOOS=windows GOARCH=amd64 go build -o p2p_client.exe .

# Linux ARM64 (e.g. Oracle VPS)
GOOS=linux GOARCH=arm64 go build -o p2p_client_arm64 .
```

---

## Notes

- The rendezvous server only sees UDP addresses during the handshake — it never touches the actual P2P traffic
- Hole punching works with most residential NATs including CGNAT
- If both peers are behind symmetric NAT, hole punching may fail — a relay would be required as fallback
- The connection uses keepalive packets every 10 seconds to keep the NAT entry alive
- If no keepalive is received for 30 seconds, the client assumes the peer disconnected and exits

---

## Roadmap

- [x] UDP hole punching
- [x] STUN-based public address discovery
- [x] Long polling signaling server
- [x] Password-protected sessions
- [x] Keepalive to maintain NAT entries
- [x] Disconnect detection
- [x] Self-hostable server binary
- [ ] End-to-end encryption (X25519 + AES-GCM)
- [ ] MITM protection via key fingerprint verification
- [ ] Reliable delivery over UDP (ACK + retransmission)
- [ ] Multi-peer mesh sessions
- [ ] VPN mode via tun/tap interface