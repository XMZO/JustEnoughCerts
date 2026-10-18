//go:build !no_onepanel

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/onepanel"

func init() { modules = append(modules, onepanel.New()) }
