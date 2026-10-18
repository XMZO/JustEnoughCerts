//go:build !no_cloudflare

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/cloudflare"

func init() { modules = append(modules, cloudflare.New()) }
