// Package migrations embeds the SQL schema files.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
