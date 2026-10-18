package prepare

import "embed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

const databaseApplicationID = 0x41585031 // AXP1
