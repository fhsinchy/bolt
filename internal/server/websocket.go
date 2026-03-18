package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

const wsWriteTimeout = 5 * time.Second

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Restrict origins to localhost. Browser extensions connect from
		// chrome-extension:// or moz-extension:// which are not subject
		// to CORS origin checks. Permissive origins are not needed and
		// would widen the attack surface if the auth token leaked.
		OriginPatterns: []string{
			"http://localhost:*",
			"http://127.0.0.1:*",
			"https://localhost:*",
			"https://127.0.0.1:*",
		},
	})
	if err != nil {
		slog.Error("websocket accept", "error", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// We don't read from the client; close the read side.
	conn.CloseRead(r.Context())

	id, ch := s.svc.RegisterClient()
	defer s.svc.UnregisterClient(id)

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}

			writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err = conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
