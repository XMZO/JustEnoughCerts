//go:build !no_cpanel

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/cpanel"

func init() { modules = append(modules, cpanel.New()) }
