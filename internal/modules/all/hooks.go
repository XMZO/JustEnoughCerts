//go:build !no_hooks

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/hooks"

func init() { modules = append(modules, hooks.New()) }
