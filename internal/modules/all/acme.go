//go:build !no_acme

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/acme"

func init() { modules = append(modules, acme.New()) }
