//go:build !no_plugins

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/plugins"

func init() { modules = append(modules, plugins.New()) }
