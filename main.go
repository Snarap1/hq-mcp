// Command hq-mcp is an MCP server for read-only database access.
package main

import (
	"context"
	"log"
	"os"

	"hq-mcp/internal/server"
)

func main() {
	// stdout is the MCP protocol channel: every diagnostic goes to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	if err := server.Serve(context.Background()); err != nil {
		log.Printf("hq-mcp: %v", err)
		os.Exit(1)
	}
}
