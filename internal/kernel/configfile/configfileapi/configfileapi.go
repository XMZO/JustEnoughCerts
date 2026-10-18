// Package configfileapi is the HTTP API of configuration files
// (configfile): export, with or without secrets; a check of what a file
// would change; and import.
package configfileapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
)

// maxFile bounds the file an import reads.
const maxFile = 1 << 20

// Options are what the operations use besides the sections.
type Options struct {
	// Confirm checks the password of the signed-in account, as
	// auth.Service.ConfirmPassword does.
	Confirm func(ctx context.Context, password string) error
	Now     func() time.Time
	Log     *slog.Logger
}

type fileOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	CacheControl       string `header:"Cache-Control"`
	Body               []byte
}

type exportInput struct {
	Body struct {
		Password string `json:"password" maxLength:"1024" doc:"The password of the signed-in account"`
	}
}

type fileInput struct {
	Body struct {
		File string `json:"file" maxLength:"1048576" doc:"The YAML file"`
	}
}

type importOutput struct {
	Body configfile.Report
}

// Register adds GET /config, POST /config/export, POST /config/check and
// POST /config/import to a.
func Register(a huma.API, r *configfile.Registry, o Options) {
	tags := []string{"config"}
	yamlFile := map[string]*huma.Response{"200": {
		Description: "The configuration file",
		Content:     map[string]*huma.MediaType{"application/yaml": {Schema: &huma.Schema{Type: huma.TypeString}}},
	}}
	huma.Register(a, huma.Operation{
		OperationID: "config-export", Method: http.MethodGet, Path: "/config", Tags: tags, Responses: yamlFile,
		Summary:     "Export the configuration as YAML, without secrets",
		Description: "The providers, the certificates and the settings of the modules that are on; an import of the file keeps the secrets JEC has.",
	}, func(ctx context.Context, _ *struct{}) (*fileOutput, error) {
		return export(ctx, r, o, false)
	})

	huma.Register(a, huma.Operation{
		OperationID: "config-export-secrets", Method: http.MethodPost, Path: "/config/export", Tags: tags, Responses: yamlFile,
		Summary: "Export the configuration as YAML with the secrets of the providers, once the password is confirmed",
	}, func(ctx context.Context, in *exportInput) (*fileOutput, error) {
		if err := o.Confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		u, _ := auth.UserFrom(ctx)
		o.Log.Info("configuration exported with secrets", "username", u.Username)
		return export(ctx, r, o, true)
	})

	huma.Register(a, huma.Operation{
		OperationID: "config-check", Method: http.MethodPost, Path: "/config/check", Tags: tags, MaxBodyBytes: 2 * maxFile,
		Summary:     "Tell what importing a configuration file would change, changing nothing",
		Description: "A file that cannot be read answers 422.",
	}, func(ctx context.Context, in *fileInput) (*importOutput, error) {
		return importFile(ctx, r, in.Body.File, false)
	})

	huma.Register(a, huma.Operation{
		OperationID: "config-import", Method: http.MethodPost, Path: "/config/import", Tags: tags, MaxBodyBytes: 2 * maxFile,
		Summary: "Import a configuration file",
		Description: "Adds what JEC lacks and changes what it has, by name; deletes nothing. Nothing changes while an entry " +
			"has an error: the report tells, and applied is false. A file that cannot be read answers 422.",
	}, func(ctx context.Context, in *fileInput) (*importOutput, error) {
		return importFile(ctx, r, in.Body.File, true)
	})
}

func importFile(ctx context.Context, r *configfile.Registry, file string, apply bool) (*importOutput, error) {
	rep, err := r.Import(ctx, []byte(file), apply)
	if fe, ok := errors.AsType[*configfile.FileError](err); ok {
		return nil, api.NewError(http.StatusUnprocessableEntity, fe.Key, fe.Params)
	}
	if err != nil {
		return nil, err
	}
	return &importOutput{Body: *rep}, nil
}

func export(ctx context.Context, r *configfile.Registry, o Options, secrets bool) (*fileOutput, error) {
	data, err := r.Export(ctx, configfile.Export{Secrets: secrets})
	if err != nil {
		return nil, err
	}
	return &fileOutput{
		ContentType:        "application/yaml",
		ContentDisposition: `attachment; filename="jec-config-` + o.Now().Format("20060102-150405") + `.yaml"`,
		CacheControl:       "no-store",
		Body:               data,
	}, nil
}
