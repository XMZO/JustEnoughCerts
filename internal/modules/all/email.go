//go:build !no_email

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/email"

func init() { modules = append(modules, email.New()) }
