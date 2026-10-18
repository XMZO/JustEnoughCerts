//go:build !no_kubernetes

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/kubernetes"

func init() { modules = append(modules, kubernetes.New()) }
