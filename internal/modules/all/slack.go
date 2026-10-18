//go:build !no_slack

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/slack"

func init() { modules = append(modules, slack.New()) }
