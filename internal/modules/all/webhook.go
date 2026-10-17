//go:build !no_webhook

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/webhook"

func init() { modules = append(modules, webhook.New()) }
