//go:build !no_namecheap

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/namecheap"

func init() { modules = append(modules, namecheap.New()) }
