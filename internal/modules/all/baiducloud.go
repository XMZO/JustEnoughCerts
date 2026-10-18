//go:build !no_baiducloud

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/baiducloud"

func init() { modules = append(modules, baiducloud.New()) }
