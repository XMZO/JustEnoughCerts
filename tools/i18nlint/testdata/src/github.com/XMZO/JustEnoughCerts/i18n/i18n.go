// Package i18n is a stand-in for the real package: the analyzer leaves
// everything in it alone, generated constants included.
package i18n

type Key string

const KernelErrorInternal Key = "kernel.error.internal"

var fromLiteral Key = "allowed.in.this.package"

func T(k Key) string { return string(k) }

func use() { _ = T(fromLiteral) }
