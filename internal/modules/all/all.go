// Package all lists the modules compiled into jec. Each module adds itself
// from a file of its own here, <id>.go, guarded by the build tag no_<id>,
// so that leaving one out takes "-tags no_<id>" or deleting that file:
//
//	//go:build !no_example
//
//	package all
//
//	import "github.com/XMZO/JustEnoughCerts/internal/modules/example"
//
//	func init() { modules = append(modules, example.New()) }
//
// "go run ./tools/new-module" writes such a file along with the module.
package all

import "github.com/XMZO/JustEnoughCerts/internal/kernel/module"

var modules []module.Module

// Modules returns the modules compiled in.
func Modules() []module.Module { return modules }
