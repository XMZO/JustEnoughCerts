//go:build !no_discord

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/discord"

func init() { modules = append(modules, discord.New()) }
