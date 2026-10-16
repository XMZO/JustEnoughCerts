// Package migrations holds the kernel's own schema migrations. Modules keep
// theirs next to their code.
package migrations

import "embed"

// FS holds the kernel's *.sql migrations, applied in version order.
//
//go:embed *.sql
var FS embed.FS
