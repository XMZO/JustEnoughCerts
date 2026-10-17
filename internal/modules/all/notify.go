//go:build !no_notify

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/notify"

func init() { modules = append(modules, notify.New()) }
