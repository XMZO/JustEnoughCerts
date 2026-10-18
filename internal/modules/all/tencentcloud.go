//go:build !no_tencentcloud

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/tencentcloud"

func init() { modules = append(modules, tencentcloud.New()) }
