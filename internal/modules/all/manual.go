//go:build !no_manual

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/manual"

func init() { modules = append(modules, manual.New()) }
