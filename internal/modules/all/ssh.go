//go:build !no_ssh

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/ssh"

func init() { modules = append(modules, ssh.New()) }
