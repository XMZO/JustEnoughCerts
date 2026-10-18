//go:build !no_synology

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/synology"

func init() { modules = append(modules, synology.New()) }
