package db

import "embed"

// Files supplies schema and seed SQL to the application management binary.
//
//go:embed migrations/*.sql seeds/*.sql
var Files embed.FS
