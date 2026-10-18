//go:build !no_ntfy

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/ntfy"

func init() { modules = append(modules, ntfy.New()) }
