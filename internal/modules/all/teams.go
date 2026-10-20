//go:build !no_teams

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/teams"

func init() { modules = append(modules, teams.New()) }
