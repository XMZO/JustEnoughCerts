//go:build !no_telegram

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/telegram"

func init() { modules = append(modules, telegram.New()) }
