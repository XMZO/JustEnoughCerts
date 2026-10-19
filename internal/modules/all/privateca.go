//go:build !no_privateca

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/privateca"

func init() { modules = append(modules, privateca.New()) }
