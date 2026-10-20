//go:build !no_offsite

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/offsite"

func init() { modules = append(modules, offsite.New()) }
