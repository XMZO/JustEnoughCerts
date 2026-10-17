//go:build !no_files

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/files"

func init() { modules = append(modules, files.New()) }
