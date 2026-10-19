//go:build !no_metrics

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/metrics"

func init() { modules = append(modules, metrics.New()) }
