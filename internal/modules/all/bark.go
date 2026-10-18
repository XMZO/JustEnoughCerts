//go:build !no_bark

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/bark"

func init() { modules = append(modules, bark.New()) }
