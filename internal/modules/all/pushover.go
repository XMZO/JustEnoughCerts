//go:build !no_pushover

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/pushover"

func init() { modules = append(modules, pushover.New()) }
