package i18n

import "strings"

// PluginText is the key of a text that a plugin brings along: under
// plugins.texts, the plugin's ID, then where the text goes, such as the
// name of one of its types. Such keys are not in the locale files: the
// plugins module hands their texts to the web UI along with the plugin's
// provider types.
func PluginText(plugin string, path ...string) Key {
	return Key("plugins.texts." + plugin + "." + strings.Join(path, "."))
}
