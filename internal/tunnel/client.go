package tunnel

import (
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// Client runs on the Docker host and dials out to the tunnel server.
type Client struct {
	serverAddr string // e.g. ws://localhost:9001/ws
	token      string
}

func NewClient(serverAddr, token string) *Client {
	return &Client{
		serverAddr: serverAddr,
		token:      token,
	}
}

func (c *Client) Start() error {
	for {
		err := c.connectAndServe()
		if err != nil {
			log.Printf("Tunnel Client disconnected: %v. Retrying in 5s...", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func (c *Client) connectAndServe() error {
	u, err := url.Parse(c.serverAddr)
	if err != nil {
		return err
	}
	if c.token != "" {
		q := u.Query()
		q.Set("token", c.token)
		u.RawQuery = q.Encode()
	}

	log.Printf("Connecting to Tunnel Server: %s", u.String())
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()

	conn := NewWebSocketConn(ws)

	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.EnableKeepAlive = true
	yamuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := yamux.Client(conn, yamuxConfig)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	log.Printf("Connected to Tunnel Server. Waiting for connections...")

	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		go c.handleStream(stream)
	}
}

func (c *Client) handleStream(stream net.Conn) {
	defer func() { _ = stream.Close() }()

	var targetAddr strings.Builder
	buf := make([]byte, 1)
	for {
		_, err := stream.Read(buf)
		if err != nil {
			log.Printf("Failed to read header: %v", err)
			return
		}
		if buf[0] == '\n' {
			break
		}
		_ = targetAddr.WriteByte(buf[0])
		if targetAddr.Len() > 256 {
			log.Printf("Header too long")
			return
		}
	}

	addr := targetAddr.String()
	log.Printf("Tunnel request for target: %s", addr)

	target, err := net.Dial("tcp", addr)
	if err != nil {
		log.Printf("Failed to dial target %s: %v", addr, err)
		return
	}
	defer func() { _ = target.Close() }()

	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(stream, target)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(target, stream)
		errChan <- err
	}()
	<-errChan
}
