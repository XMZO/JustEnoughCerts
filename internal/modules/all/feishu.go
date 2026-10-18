//go:build !no_feishu

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/feishu"

func init() { modules = append(modules, feishu.New()) }
