//go:build !no_monitor

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/monitor"

func init() { modules = append(modules, monitor.New()) }
