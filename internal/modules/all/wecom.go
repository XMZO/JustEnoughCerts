//go:build !no_wecom

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/wecom"

func init() { modules = append(modules, wecom.New()) }
