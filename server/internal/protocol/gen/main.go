// Command gen writes the JSON Schemas of the protocol and the capability catalog.
// Run via `go generate ./...` in server/; output goes to /catalog.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Humbarrt/kicker/server/internal/protocol"
)

func main() {
	out := flag.String("out", "catalog", "output folder")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	write(filepath.Join(*out, fmt.Sprintf("protocol-v%d.schema.json", protocol.Version)), protocol.ProtocolSchema())
	for _, c := range protocol.Catalog {
		write(filepath.Join(*out, c.Key()+".schema.json"), protocol.CapabilitySchema(c))
	}
}

func write(path string, doc map[string]any) {
	b, err := protocol.MarshalSchema(doc)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fail(err)
	}
	fmt.Println("wrote", path)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}
