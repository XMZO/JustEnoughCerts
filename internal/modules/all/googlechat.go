//go:build !no_googlechat

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/googlechat"

func init() { modules = append(modules, googlechat.New()) }
