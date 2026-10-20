//go:build !no_pushplus

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/pushplus"

func init() { modules = append(modules, pushplus.New()) }
