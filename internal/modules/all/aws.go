//go:build !no_aws

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/aws"

func init() { modules = append(modules, aws.New()) }
