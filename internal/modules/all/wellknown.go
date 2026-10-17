//go:build !no_wellknown

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/wellknown"

func init() { modules = append(modules, wellknown.New()) }
