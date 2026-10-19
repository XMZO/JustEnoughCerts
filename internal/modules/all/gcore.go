//go:build !no_gcore

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/gcore"

func init() { modules = append(modules, gcore.New()) }
