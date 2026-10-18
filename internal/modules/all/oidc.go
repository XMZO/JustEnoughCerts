//go:build !no_oidc

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/oidc"

func init() { modules = append(modules, oidc.New()) }
