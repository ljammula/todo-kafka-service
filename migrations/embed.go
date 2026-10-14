// Package migrations embeds the SQL files consumed by the db-migrate command.
package migrations

import "embed"

// Files contains versioned up/down migration pairs.
//
//go:embed *.sql
var Files embed.FS
