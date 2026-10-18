//go:build !no_upyun

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/upyun"

func init() { modules = append(modules, upyun.New()) }
