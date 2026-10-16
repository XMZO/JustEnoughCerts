package main

import "github.com/XMZO/JustEnoughCerts/internal/kernel/module"

// modules are the modules compiled into the binary. Each module adds itself
// from a file of its own, module_<id>.go, guarded by the build tag
// no_<id>, so that leaving one out takes "-tags no_<id>" or deleting that
// file:
//
//	//go:build !no_example
//
//	package main
//
//	import "github.com/XMZO/JustEnoughCerts/internal/modules/example"
//
//	func init() { modules = append(modules, example.New()) }
var modules []module.Module
