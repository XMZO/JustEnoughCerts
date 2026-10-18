//go:build !no_audit

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/audit"

func init() { modules = append(modules, audit.New()) }
