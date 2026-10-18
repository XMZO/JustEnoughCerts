//go:build !no_serverchan

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/serverchan"

func init() { modules = append(modules, serverchan.New()) }
