//go:build !no_proxmox

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/proxmox"

func init() { modules = append(modules, proxmox.New()) }
