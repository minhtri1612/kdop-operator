package tunnel

import (
	"io"
	"log"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

// Server is the tunnel server (runs in-cluster later).
type Server struct {
	listenAddr string // TCP for Service traffic, e.g. :9000
	wsAddr     string // WebSocket for clients — used in 3.3
	authToken  string
	sessions   []*yamux.Session
	targets    []string
	rrIndex    uint64
	mu         sync.Mutex
}

func NewServer(listenAddr, wsAddr, authToken string, targets []string) *Server {
	return &Server{
		listenAddr: listenAddr,
		wsAddr:     wsAddr,
		authToken:  authToken,
		targets:    targets,
		sessions:   make([]*yamux.Session, 0),
	}
}

// Start listens for TCP traffic. WebSocket comes in step 3.3.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	log.Printf("Listening for TCP traffic on %s", s.listenAddr)
	go s.acceptTCP(ln)

	// Block until process exit (3.3 will replace this with http.ListenAndServe)
	select {}
}

func (s *Server) acceptTCP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Accept TCP error: %v", err)
			continue
		}
		go s.handleTCP(conn)
	}
}

func (s *Server) handleTCP(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("handleTCP PANIC: %v", r)
		}
	}()
	defer func() { _ = conn.Close() }()
	log.Printf("handleTCP: Accepted connection from %s", conn.RemoteAddr())

	s.mu.Lock()
	if len(s.sessions) == 0 {
		s.mu.Unlock()
		log.Println("handleTCP: No tunnel client connected, dropping connection")
		return
	}
	if len(s.targets) == 0 {
		s.mu.Unlock()
		log.Println("handleTCP: No targets configured, dropping connection")
		return
	}

	targetIdx := s.rrIndex % uint64(len(s.targets))
	target := s.targets[targetIdx]
	sessionIdx := s.rrIndex % uint64(len(s.sessions))
	sess := s.sessions[sessionIdx]
	s.rrIndex++
	s.mu.Unlock()

	log.Printf("handleTCP: Selected target %s via session %d", target, sessionIdx)

	stream, err := sess.Open()
	if err != nil {
		log.Printf("handleTCP: Failed to open stream: %v", err)
		return
	}
	defer func() { _ = stream.Close() }()

	// Header: "IP:Port\n" — client dials this address
	if _, err := stream.Write([]byte(target + "\n")); err != nil {
		log.Printf("handleTCP: Failed to write target header: %v", err)
		return
	}

	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(stream, conn)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(conn, stream)
		errChan <- err
	}()

	err = <-errChan
	log.Printf("handleTCP: Connection closed: %v", err)
}
