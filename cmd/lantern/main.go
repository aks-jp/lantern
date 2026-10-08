// Command lantern is an MCP server that exposes a SearXNG instance as web search tools.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("lantern", version)
		return
	}
	fmt.Fprintln(os.Stderr, "lantern: not implemented yet")
	os.Exit(1)
}
