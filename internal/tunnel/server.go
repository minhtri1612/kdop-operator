package tunnel

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// Server is the tunnel server (runs in-cluster later).
type Server struct {
	listenAddr   string
	wsAddr       string
	authToken    string
	sessions     []*yamux.Session
	targets      []string
	pushTargets  []string
	targetsFile  string
	lastPushTime time.Time
	rrIndex      uint64
	mu           sync.Mutex
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

func (s *Server) SetTargetsFile(path string) {
	s.mu.Lock()
	s.targetsFile = path
	s.mu.Unlock()
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	log.Printf("Listening for TCP traffic on %s", s.listenAddr)
	go s.acceptTCP(ln)

	if s.targetsFile != "" {
		go s.watchTargetsFile()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/reload", s.handleReload)
	log.Printf("Listening for Tunnel Clients on %s", s.wsAddr)
	if s.authToken != "" {
		log.Printf("Tunnel authentication enabled")
	} else {
		log.Printf("WARNING: Tunnel authentication disabled (no auth token set)")
	}
	return http.ListenAndServe(s.wsAddr, mux)
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("handleWS PANIC: %v", rec)
		}
	}()

	if s.authToken != "" {
		token := r.URL.Query().Get("token")
		if token != s.authToken {
			log.Printf("Tunnel auth failed from %s: invalid token", r.RemoteAddr)
			http.Error(w, "Forbidden: invalid tunnel auth token", http.StatusForbidden)
			return
		}
		log.Printf("Tunnel auth succeeded from %s", r.RemoteAddr)
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS Upgrade error: %v", err)
		return
	}

	log.Printf("Tunnel Client connected from %s", ws.RemoteAddr())
	conn := NewWebSocketConn(ws)

	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.EnableKeepAlive = true
	yamuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := yamux.Server(conn, yamuxConfig)
	if err != nil {
		log.Printf("Yamux Server error: %v", err)
		_ = conn.Close()
		return
	}

	s.mu.Lock()
	s.sessions = append(s.sessions, session)
	currentCount := len(s.sessions)
	s.mu.Unlock()
	log.Printf("Registered new session. Total active sessions: %d", currentCount)

	defer func() {
		s.mu.Lock()
		for i, sess := range s.sessions {
			if sess == session {
				s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
				break
			}
		}
		remaining := len(s.sessions)
		s.mu.Unlock()
		_ = session.Close()
		log.Printf("Tunnel Client disconnected. Remaining sessions: %d", remaining)
	}()

	for !session.IsClosed() {
		time.Sleep(1 * time.Second)
	}
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targets := r.URL.Query().Get("targets")
	if targets != "" {
		var newTargets []string
		for t := range strings.SplitSeq(targets, ",") {
			if tVal := strings.TrimSpace(t); tVal != "" {
				newTargets = append(newTargets, tVal)
			}
		}
		s.mu.Lock()
		s.targets = newTargets
		s.pushTargets = newTargets
		s.lastPushTime = time.Now()
		s.mu.Unlock()
		log.Printf("Targets reloaded via Push: %v", newTargets)
	} else {
		log.Println("Targets reload triggered via API (Pulling from file)")
		s.loadTargetsFromFile()
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) watchTargetsFile() {
	log.Printf("Watching targets file: %s", s.targetsFile)
	s.loadTargetsFromFile()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.loadTargetsFromFile()
	}
}

func (s *Server) loadTargetsFromFile() {
	data, err := os.ReadFile(s.targetsFile)
	if err != nil {
		log.Printf("Error reading targets file %s: %v", s.targetsFile, err)
		return
	}

	content := strings.TrimSpace(string(data))
	var newTargets []string
	if content != "" {
		for t := range strings.SplitSeq(content, ",") {
			if tVal := strings.TrimSpace(t); tVal != "" {
				newTargets = append(newTargets, tVal)
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.lastPushTime.IsZero() && time.Since(s.lastPushTime) < 2*time.Minute {
		if !targetsEqual(newTargets, s.pushTargets) {
			return
		}
	}

	if targetsEqual(newTargets, s.targets) {
		return
	}

	log.Printf("Dynamic targets updated from file: %v", newTargets)
	s.targets = newTargets
}

func targetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
