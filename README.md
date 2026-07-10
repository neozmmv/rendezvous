# Rendezvous

A lightweight, self-hostable **signaling server** for establishing direct P2P connections via UDP hole punching (blindspot).

> **The server is only used to exchange UDP addresses (and pin static public keys) between peers. Once the connection is established, all traffic flows directly between peers — the server is no longer involved.**

Rendezvous is the signaling companion to **[Blindspot](https://github.com/neozmmv/blindspot)** — a P2P VPN and networking toolkit built on UDP hole punching with end-to-end encryption (Noise `IKpsk2`, the same cipher suite as WireGuard). Blindspot handles the encrypted transport; Rendezvous only brokers the initial peer discovery.

A public instance is available at `https://rendezvous.enzogp.dev`. You can also self-host your own instance.

---

## How it works

```
Peer A                  Rendezvous Server                 Peer B
  |                           |                              |
  |-- discovers public IP via STUN (Google)                 |
  |-- POST /session/abc ----->|                              |
  |   (opens SSE stream)      |<----- POST /session/abc -----|
  |                           |    (server crosses addresses)|
  |<-- peer B's addr + key ---|------ peer A's addr + key -->|
  |                           |                              |
  |<============= UDP hole punching (direct) ===============>|
  |                           |                              |
  |<===== encrypted P2P connection (Noise, in Blindspot) ===>|
```

1. Each peer discovers its public IP:port via STUN
2. Both peers register their UDP address (and Noise static public key) on the rendezvous server using the same session ID
3. The server exchanges their addresses and each peer learns the other's key and address; both start hole punching simultaneously
4. Peers open a Server-Sent Events stream to be notified of members that join later
5. Once the connection is established, the server is no longer needed

Because the rendezvous is fronted by TLS, it acts as a trusted anchor of identity: it distributes each peer's **public** key so peers can pin each other before the Noise handshake, defeating on-path impersonation. The key is public by definition — only its integrity in transit matters, which TLS provides.

---

## Self-hosting

You can run your own rendezvous server. Pre-built binaries for Linux (amd64/arm64), Windows, and macOS (amd64/arm64) are available on the [releases page](https://github.com/neozmmv/rendezvous/releases) — no Docker required.

### Option 1 — Binary + Cloudflare Tunnel (recommended)

No public IP or port forwarding required.

```bash
# download the server binary for your platform from the releases page
chmod +x rendezvous-linux-amd64
./rendezvous-linux-amd64   # listens on :8000

# in another terminal, expose it via Cloudflare Tunnel
cloudflared tunnel --url http://localhost:8000
```

Cloudflare will print a public URL — use that as your hostname in the client.

### Option 2 — Docker + Cloudflare Tunnel

**Requirements**

- Docker + Docker Compose
- [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)

**docker-compose.yml**

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

### Build from source

```bash
git clone https://github.com/neozmmv/rendezvous
cd rendezvous
go build -o rendezvous .
./rendezvous
```

---

## API

The server keeps all session state in memory. Requests are rate limited per client IP.

### `POST /session/:id`

Simple session without a password. Registering returns the peers already present; the same call refreshes the caller's presence (heartbeat) and resets its TTL.

**Request:**
```json
{
  "udp_addr": "191.176.32.57:51740",
  "local_addr": "192.168.1.20:51740",
  "pub_key": "base64-encoded 32-byte Noise static public key"
}
```

**Response (peers already present, excluding the caller):**
```json
{
  "peers": [
    {
      "ip": "201.x.x.x:55321",
      "local_addr": "192.168.1.31:55321",
      "pub_key": "base64-encoded 32-byte Noise static public key"
    }
  ]
}
```

> `pub_key` carries each peer's Noise static public key. The same `{ip, local_addr, pub_key}` shape is emitted by every `peer` event on the SSE `.../stream` endpoints. `pub_key` is optional — plain hole-punching clients can omit it.

---

### `GET /session/:id/stream`

Server-Sent Events stream of peers for a session. Emits a `peer` event (shape above) for each member already present and for every member that joins later. Sends a `: keepalive` comment every 30 seconds to keep proxies from closing the connection.

```
GET /session/my-session/stream?udp_addr=191.176.32.57:51740
```

`udp_addr` identifies the caller so it isn't streamed its own entry. Returns `404` if the session does not exist.

---

### `POST /session/:id/leave`

Removes the caller from the session. When the last peer leaves, the session is deleted.

**Request:**
```json
{ "udp_addr": "191.176.32.57:51740" }
```

---

### `POST /create_session`

Creates a password-protected session. Must be called before peers can join.

**Request:**
```json
{
  "id": "my-network",
  "password": "secret",
  "max_peers": 4
}
```

**Response:**
```json
{ "message": "session created" }
```

> `max_peers` is optional; `0` (or omitted) means unlimited. A newly created session expires automatically after 5 minutes if no peers join.

---

### `POST /join_session/:id`

Joins a password-protected session. Works like `/session/:id` but requires the password, and honors the session's `max_peers` limit.

**Request:**
```json
{
  "udp_addr": "191.176.32.57:51740",
  "local_addr": "192.168.1.20:51740",
  "password": "secret",
  "pub_key": "base64-encoded 32-byte Noise static public key"
}
```

**Response:** same `{ "peers": [...] }` shape as `/session/:id`.

**Error responses:**
```json
{ "error": "session not found" }
{ "error": "incorrect password" }
{ "error": "session is full" }
```

---

### `GET /join_session/:id/stream`

SSE stream of peers for a password session. The password is supplied in the
**`Authorization` header** (`Authorization: Bearer <password>`), never in the query
string, so it does not leak into access logs or proxies. A wrong or missing
token returns `401`.

```
GET /join_session/my-network/stream?udp_addr=191.176.32.57:51740
Authorization: Bearer <password>
```

---

### `POST /join_session/:id/leave`

Removes the caller from a password session. When the last peer leaves, the session is deleted.

**Request:**
```json
{ "udp_addr": "191.176.32.57:51740" }
```

---

### `GET /version` and `GET /`

`GET /version` returns `{ "version": "<build version>" }`. `GET /` returns a welcome
message with the caller's `clientIP` and the server `version`. The version is baked
in at build time via `-ldflags "-X main.Version=..."`.

---

## Client

The reference client is **[Blindspot](https://github.com/neozmmv/blindspot)**, which drives
the full flow against a rendezvous server: STUN discovery, signaling, key pinning,
hole punching, the Noise `IKpsk2` handshake, encrypted transport, keepalive, and
disconnect detection. Point it at `https://rendezvous.enzogp.dev` or your own instance.

The `client/` directory in this repo is a minimal, unencrypted demo used during early
development. It is not published in releases and is not kept in lockstep with the
current server API — treat it as a reference for the raw hole-punching mechanics only.

---

## Notes

- The rendezvous server only sees UDP addresses and public keys during the handshake — it never touches the actual P2P traffic
- Hole punching works with most residential NATs including CGNAT
- If both peers are behind symmetric NAT, hole punching may fail — a relay would be required as fallback
- Sessions live entirely in memory: a server restart drops all sessions
- A peer that stops re-registering is evicted after a 10-minute TTL; empty sessions are deleted

---

## Roadmap

- [x] UDP hole punching
- [x] STUN-based public address discovery
- [x] Signaling server (register + Server-Sent Events streaming)
- [x] Password-protected sessions
- [x] Multi-peer sessions with `max_peers` limit
- [x] Peer TTL and automatic session expiry
- [x] Leave endpoints for clean teardown
- [x] Per-IP rate limiting
- [x] Static public key distribution for pinning (Noise handshake in Blindspot)
- [x] Self-hostable server binaries (Linux, Windows, macOS)
- [ ] Persistent session store (survive restarts)
