//go:build !no_gotify

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/gotify"

func init() { modules = append(modules, gotify.New()) }
