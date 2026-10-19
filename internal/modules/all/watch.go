//go:build !no_watch

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/watch"

func init() { modules = append(modules, watch.New()) }
