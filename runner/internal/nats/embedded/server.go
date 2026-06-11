// Package embedded starts and manages an in-process NATS server for
// development and testing scenarios where an external NATS cluster is
// not available.
package embedded

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// Server wraps an embedded NATS server instance.
type Server struct {
	srv     *server.Server
	Port    int
	URL     string
}

// Start creates and starts an embedded NATS server on a random port.
func Start() (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("find free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	opts := &server.Options{
		Port:       port,
		NoSigs:     true,
		ServerName: "devfleet-embedded",
	}
	nc, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("create nats server: %w", err)
	}
	nc.ConfigureLogger()
	go nc.Start()
	if !nc.ReadyForConnections(10 * time.Second) {
		nc.Shutdown()
		return nil, fmt.Errorf("nats server did not start in time")
	}
	url := fmt.Sprintf("nats://localhost:%d", port)
	slog.Info("embedded server started", "component", "nats", "url", url)
	return &Server{srv: nc, Port: port, URL: url}, nil
}

// Close shuts down the embedded NATS server.
func (s *Server) Close() {
	if s.srv != nil {
		s.srv.Shutdown()
	}
}

// ClientURL returns the full NATS URL clients should connect to.
func (s *Server) ClientURL() string {
	return s.URL
}

// PortStr returns the server's port as a string.
func (s *Server) PortStr() string {
	return strconv.Itoa(s.Port)
}
