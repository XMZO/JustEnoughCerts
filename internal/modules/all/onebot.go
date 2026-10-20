//go:build !no_onebot

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/onebot"

func init() { modules = append(modules, onebot.New()) }
