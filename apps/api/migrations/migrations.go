// Package migrations embeds the goose SQL migrations so the api binary can
// migrate its own database. sqlc also reads these files as the schema.
package migrations

import "embed"

// FS holds every migration file in this directory.
//
//go:embed *.sql
var FS embed.FS
