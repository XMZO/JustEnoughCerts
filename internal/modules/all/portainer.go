//go:build !no_portainer

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/portainer"

func init() { modules = append(modules, portainer.New()) }
