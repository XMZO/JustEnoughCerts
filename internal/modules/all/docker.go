//go:build !no_docker

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/docker"

func init() { modules = append(modules, docker.New()) }
