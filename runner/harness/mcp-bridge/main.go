// mcp-bridge bridges OpenCode's stdio MCP to a runner Unix socket MCP.
// OpenCode forks this binary, sends JSON-RPC on stdin, receives on stdout.
// This binary forwards those bytes to/from the Unix socket at the given path.
package main

import (
	"io"
	"log"
	"net"
	"os"
	"sync"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: mcp-bridge <unix-socket-path>")
	}
	socketPath := os.Args[1]

	// Connect to the runner's MCP Unix socket.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalf("dial %s: %v", socketPath, err)
	}
	defer conn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// stdin → socket
	go func() {
		defer wg.Done()
		if _, err := io.Copy(conn, os.Stdin); err != nil {
			log.Printf("stdin->socket: %v", err)
		}
	}()

	// socket → stdout
	go func() {
		defer wg.Done()
		if _, err := io.Copy(os.Stdout, conn); err != nil {
			log.Printf("socket->stdout: %v", err)
		}
	}()

	wg.Wait()
}
