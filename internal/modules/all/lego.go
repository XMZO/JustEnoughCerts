//go:build !no_lego

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/lego"

func init() { modules = append(modules, lego.New()) }
