# internal/server

HTTP server with REST API and WebSocket for real-time progress. Phase 2 core.

## Files

| File | Purpose |
|---|---|
| `server.go` | Server struct, Start/Shutdown, route registration, JSON helpers |
| `handlers.go` | REST endpoint handlers |
| `websocket.go` | WebSocket handler + event-to-message conversion |
| `middleware.go` | recovery, logging, auth middleware |
| `server_test.go` | Tests using httptest |

## Routes

| Method | Path | Handler |
|---|---|---|
| POST | `/api/downloads` | Add download |
| GET | `/api/downloads` | List downloads |
| GET | `/api/downloads/{id}` | Get download detail |
| DELETE | `/api/downloads/{id}` | Delete download |
| POST | `/api/downloads/{id}/pause` | Pause download |
| POST | `/api/downloads/{id}/resume` | Resume download |
| POST | `/api/downloads/{id}/retry` | Retry failed download |
| POST | `/api/downloads/{id}/refresh` | Refresh URL |
| PUT | `/api/downloads/reorder` | Reorder queue |
| GET | `/api/config` | Get config (sans auth token) |
| PUT | `/api/config` | Update config |
| GET | `/api/stats` | Get stats |
| POST | `/api/probe` | Probe URL |
| GET | `/ws` | WebSocket for real-time events |

## Auth

- REST: `Authorization: Bearer <token>` header
- WebSocket: `?token=<token>` query parameter

## Middleware Chain

recovery -> logging -> auth

## WebSocket Messages

Events from the bus are forwarded as JSON with a `type` field matching the event type string.
