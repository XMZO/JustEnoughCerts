//go:build !no_esa

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/esa"

func init() { modules = append(modules, esa.New()) }
