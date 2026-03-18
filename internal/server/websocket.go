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
		// SAFETY: Origin checking is meaningless over Unix socket;
		// filesystem permissions are the trust boundary. If this
		// handler is ever served on TCP, origin verification and
		// authentication must be added back.
		InsecureSkipVerify: true,
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
