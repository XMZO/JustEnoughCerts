//go:build !no_whoice

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/whoice"

func init() { modules = append(modules, whoice.New()) }
