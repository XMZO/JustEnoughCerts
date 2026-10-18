//go:build !no_dingtalk

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/dingtalk"

func init() { modules = append(modules, dingtalk.New()) }
