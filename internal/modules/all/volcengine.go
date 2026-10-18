//go:build !no_volcengine

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/volcengine"

func init() { modules = append(modules, volcengine.New()) }
