//go:build !no_aliyun

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/aliyun"

func init() { modules = append(modules, aliyun.New()) }
