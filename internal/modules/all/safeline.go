//go:build !no_safeline

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/safeline"

func init() { modules = append(modules, safeline.New()) }
