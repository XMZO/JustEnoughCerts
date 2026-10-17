//go:build !no_certs

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/certs"

func init() { modules = append(modules, certs.New()) }
