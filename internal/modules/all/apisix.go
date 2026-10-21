//go:build !no_apisix

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/apisix"

func init() { modules = append(modules, apisix.New()) }
