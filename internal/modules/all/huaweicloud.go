//go:build !no_huaweicloud

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/huaweicloud"

func init() { modules = append(modules, huaweicloud.New()) }
