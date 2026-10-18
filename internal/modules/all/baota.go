//go:build !no_baota

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/baota"

func init() { modules = append(modules, baota.New()) }
