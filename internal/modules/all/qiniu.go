//go:build !no_qiniu

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/qiniu"

func init() { modules = append(modules, qiniu.New()) }
