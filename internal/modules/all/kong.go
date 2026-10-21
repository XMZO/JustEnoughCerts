//go:build !no_kong

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/kong"

func init() { modules = append(modules, kong.New()) }
