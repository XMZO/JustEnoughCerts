package plugins

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// PluginBody is a file of the plugins directory, as JEC found it when it
// started.
type PluginBody struct {
	File        string             `json:"file"`
	ID          string             `json:"id"`
	Name        i18n.Key           `json:"name,omitempty" doc:"Message key of the name the plugin gives itself, in texts"`
	Description i18n.Key           `json:"description,omitempty"`
	Types       []string           `json:"types" doc:"The provider types it added"`
	Error       *contracts.Message `json:"error,omitempty" doc:"Why it is not in use, or not wholly"`
	// Texts holds the messages of Name and Description, by language.
	Texts map[string]map[i18n.Key]string `json:"texts,omitempty" doc:"The plugin's own messages, by language tag and key"`
}

// PluginListBody lists the plugins.
type PluginListBody struct {
	Dir     string       `json:"dir" doc:"Where JEC looks for plugins when it starts"`
	Plugins []PluginBody `json:"plugins"`
}

type listOutput struct {
	Body PluginListBody
}

func registerAPI(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "plugins-files", Method: http.MethodGet, Path: "/files",
		Summary:     "List the plugins",
		Description: "The executables of the plugins directory as JEC found them when it started, with the types each added or why it is not in use.",
	}, func(context.Context, *struct{}) (*listOutput, error) {
		out := &listOutput{Body: PluginListBody{Dir: m.dir, Plugins: []PluginBody{}}}
		for _, l := range m.found {
			b := PluginBody{File: l.file, ID: l.plugin.id, Types: append([]string{}, l.added...), Error: l.err}
			if !l.name.empty() {
				w := words{tags: m.tags, texts: map[string]map[i18n.Key]string{}}
				b.Name = w.add(l.name, l.plugin.id, "name")
				if !l.desc.empty() {
					b.Description = w.add(l.desc, l.plugin.id, "description")
				}
				b.Texts = w.texts
			}
			out.Body.Plugins = append(out.Body.Plugins, b)
		}
		return out, nil
	})
}
