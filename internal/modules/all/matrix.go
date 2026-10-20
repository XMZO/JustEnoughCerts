//go:build !no_matrix

package all

import "github.com/XMZO/JustEnoughCerts/internal/modules/matrix"

func init() { modules = append(modules, matrix.New()) }
