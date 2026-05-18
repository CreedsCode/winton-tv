# winton-tv

Discord-gated, low-latency multistream platform for the Winton community.

Self-hosted alternative to Twitch where streaming and chat are gated to verified Discord guild members, with PUG/ESL-style multistream observer coverage for community events.

## Vision

**V1 — Community streaming**
- Discord OAuth with Winton guild membership gate
- Per-user stream keys, managed from a personal dashboard
- Watch page with chat, scoped to authenticated guild members
- WebRTC sub-500ms latency end-to-end

**V2 — Observer / coverage mode**
- Rooms with multiple simultaneous publishers
- Room owner gets a drag-and-drop "director" view to compose layouts
- ESL-watch-page style POV grid for viewers
- Optional server-side composite → RTMP egress to Twitch / YouTube

## Stack

| Layer | Choice | Why |
| --- | --- | --- |
| SFU | [LiveKit](https://livekit.io) (self-hosted) | Rooms, multi-publisher, JWT auth, mature SDKs |
| Backend | Go 1.23 + [chi](https://github.com/go-chi/chi) | Matches existing infra (Broadcast Box, LiveKit are Go); single binary |
| Frontend | `html/template` + [HTMX](https://htmx.org) + LiveKit JS SDK | No node_modules, no framework — server-rendered with sprinkles |
| DB | Postgres + [pgx](https://github.com/jackc/pgx) | Standard, boring, works |
| Sessions | [scs](https://github.com/alexedwards/scs) + Postgres store | Server-side sessions, no JWT in cookies |
| Deploy | Docker Compose via Dokploy | Existing homelab setup |

## Dev quickstart

Requires Go 1.23+ and Docker.

```bash
cp .env.example .env
# edit .env if needed (defaults work for v0 landing page)

make dev
# → http://localhost:8080
```

## Docker

```bash
docker compose up --build
```

## Roadmap

See [GitHub issues](https://github.com/CreedsCode/winton-tv/issues) and the project board.

| Milestone | PR | Status |
| --- | --- | --- |
| v0 scaffold | `feat/scaffold` | In progress |
| Discord OAuth + guild gate | `feat/discord-oauth` | Pending |
| Postgres + stream key store | `feat/postgres-store` | Pending |
| LiveKit token signing + WHIP proxy | `feat/livekit-tokens` | Pending |
| Watch page + LiveKit JS viewer | `feat/watch-page` | Pending |
| Chat (LiveKit DataChannel) | `feat/chat` | Pending |
| Observer mode (V2) | `feat/observer` | Pending |
| RTMP egress (V2) | `feat/egress` | Pending |

## License

TBD — likely AGPL-3.0 (community-protective, forks must stay open).

## Contributing

This is a community project for Winton members. Open an issue first to discuss any non-trivial change.
