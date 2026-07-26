package tunnel

import (
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketConn adapts a websocket.Conn to the net.Conn interface
// so yamux can multiplex streams over one WebSocket.
type WebSocketConn struct {
	ws     *websocket.Conn
	reader io.Reader
}

func NewWebSocketConn(ws *websocket.Conn) *WebSocketConn {
	c := &WebSocketConn{ws: ws}

	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	return c
}

func (c *WebSocketConn) Read(b []byte) (n int, err error) {
	if c.reader == nil {
		_, r, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}
		c.reader = r
	}
	n, err = c.reader.Read(b)
	if err == io.EOF {
		// Message boundary: fetch next WS frame and continue
		c.reader = nil
		return c.Read(b)
	}
	return n, err
}

func (c *WebSocketConn) Write(b []byte) (n int, err error) {
	if err := c.ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *WebSocketConn) Close() error {
	return c.ws.Close()
}

func (c *WebSocketConn) LocalAddr() net.Addr {
	return c.ws.LocalAddr()
}

func (c *WebSocketConn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *WebSocketConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

func (c *WebSocketConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *WebSocketConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}

// Compile-time check: WebSocketConn must implement net.Conn
var _ net.Conn = (*WebSocketConn)(nil)
