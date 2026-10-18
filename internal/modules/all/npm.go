//go:build !no_npm

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/npm"

func init() { modules = append(modules, npm.New()) }
