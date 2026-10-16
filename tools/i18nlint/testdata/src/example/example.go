package example

import "github.com/XMZO/JustEnoughCerts/i18n"

const untyped = "kernel.error.internal"

const aliased = i18n.KernelErrorInternal

type message struct {
	key  i18n.Key
	text string
}

func calls() {
	_ = i18n.T(i18n.KernelErrorInternal)
	_ = i18n.T(aliased)
	_ = i18n.T("kernel.error.internal") // want `string literal used as an i18n.Key`
	_ = i18n.T(untyped)                 // want `untyped constant untyped used as an i18n.Key`

	s := "kernel.error.internal"
	_ = i18n.T(i18n.Key(s))       // want `conversion to i18n.Key`
	_ = i18n.T(i18n.Key("a.b.c")) // want `conversion to i18n.Key`
	_ = i18n.T(i18n.Key(untyped)) // want `conversion to i18n.Key`

	var k i18n.Key = "a.b.c" // want `string literal used as an i18n.Key`
	_ = k

	_ = message{key: "a.b.c", text: "plain strings are fine"} // want `string literal used as an i18n.Key`
	_ = message{key: i18n.KernelErrorInternal, text: "ok"}

	// Comparing the text of a key is not making one up.
	_ = string(i18n.KernelErrorInternal) == "kernel.error.internal"
	_ = string(k)
}
