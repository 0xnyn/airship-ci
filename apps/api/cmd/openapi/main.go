// Command openapi writes the API's OpenAPI 3.1 document without starting a
// server or connecting to a database. packages/client generates its
// TypeScript types from the output.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wizenheimer/airship/apps/api/internal/server"
)

func main() {
	out := flag.String("o", "", "write the document to this file instead of stdout")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string) error {
	out = filepath.Clean(out)
	api := server.NewAPI(http.NewServeMux(), server.Deps{Version: "1.0.0"})

	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		return fmt.Errorf("marshal openapi: %w", err)
	}

	var doc bytes.Buffer
	if err := json.Indent(&doc, raw, "", "  "); err != nil {
		return fmt.Errorf("indent openapi: %w", err)
	}
	doc.WriteByte('\n')

	if out == "." {
		_, err = os.Stdout.Write(doc.Bytes())

		return err
	}

	// Leave an unchanged file alone so file watchers (the client's dev task)
	// only fire when the API surface actually changed.
	if existing, err := os.ReadFile(out); err == nil && bytes.Equal(existing, doc.Bytes()) {
		return nil
	}

	if err := os.WriteFile(out, doc.Bytes(), 0o644); err != nil { //nolint:gosec // a public spec, not a secret
		return fmt.Errorf("write %s: %w", out, err)
	}

	return nil
}
