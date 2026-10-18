//go:build !no_caddy

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/caddy"

func init() { modules = append(modules, caddy.New()) }
