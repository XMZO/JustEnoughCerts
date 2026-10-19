package certs

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

type importInput struct {
	Body struct {
		Files []struct {
			Name    string `json:"name" maxLength:"255"`
			Content string `json:"content" maxLength:"1400000" doc:"The file in base64, or PEM text as it is"`
		} `json:"files" minItems:"1" maxItems:"200" doc:"Certificates and private keys in any mix, as another tool exported them"`
		RenewBeforeDays int     `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365"`
		Targets         []int64 `json:"targets,omitempty" maxItems:"50"`
	}
}

// ImportedBody is what came of a certificate found among the files.
type ImportedBody struct {
	Domains  []string  `json:"domains"`
	NotAfter time.Time `json:"notAfter"`
	Result   string    `json:"result" enum:"added,duplicate,no_key,failed" doc:"added; duplicate: JEC has it already; no_key: no private key among the files belongs to it; failed: see code"`
	ID       int64     `json:"id,omitempty" doc:"The certificate it was added as, or is already"`
	Code     string    `json:"code,omitempty" doc:"Why it failed, by message key"`
}

// FileProblemBody is a file that could not be read.
type FileProblemBody struct {
	Name string `json:"name"`
	Code string `json:"code" doc:"Why, by message key"`
}

type importOutput struct {
	Body struct {
		Certificates []ImportedBody    `json:"certificates"`
		Files        []FileProblemBody `json:"files"`
	}
}

func registerImportAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-import", Method: http.MethodPost, Path: "/certificates/import",
		Summary:     "Add the certificates among several files, each with the private key that belongs to it",
		Description: "The files may hold certificates, chains and keys in any mix; each key is paired with its certificate, whose chain is built from all the certificates found. Those JEC has already are left as they are.",
	}, func(ctx context.Context, in *importInput) (*importOutput, error) {
		targets, err := s.validTargets(ctx, in.Body.Targets)
		if err != nil {
			return nil, err
		}
		files := make([][]byte, len(in.Body.Files))
		for i, f := range in.Body.Files {
			files[i] = decodeImport(f.Content)
		}
		pairs, bad := cryptox.FindPairs(files)
		out := &importOutput{}
		out.Body.Certificates, out.Body.Files = []ImportedBody{}, []FileProblemBody{}
		for i, f := range in.Body.Files {
			if err, ok := bad[i]; ok {
				out.Body.Files = append(out.Body.Files, FileProblemBody{Name: f.Name, Code: string(fileProblem(err))})
			}
		}
		for _, b := range pairs.Bundles {
			r := ImportedBody{Domains: cryptox.Names(b.Leaf), NotAfter: b.Leaf.NotAfter.UTC(), Result: "added"}
			id, err := s.add(ctx, b, in.Body.RenewBeforeDays, targets)
			var dup *duplicateError
			switch {
			case errors.As(err, &dup):
				r.Result, r.ID = "duplicate", dup.certificate
			case err != nil:
				r.Result, r.Code = "failed", string(i18n.CertsErrorImportFailed)
				s.log.Warn("import a certificate", "domains", r.Domains, "err", err)
			default:
				r.ID = id
			}
			out.Body.Certificates = append(out.Body.Certificates, r)
		}
		for _, c := range pairs.Alone {
			out.Body.Certificates = append(out.Body.Certificates, ImportedBody{Domains: cryptox.Names(c), NotAfter: c.NotAfter.UTC(), Result: "no_key"})
		}
		return out, nil
	})
}

// fileProblem names what is wrong with a file, by message key.
func fileProblem(err error) i18n.Key {
	switch {
	case errors.Is(err, cryptox.ErrEncryptedKey):
		return i18n.CertsErrorEncryptedKey
	case errors.Is(err, cryptox.ErrBadKey), errors.Is(err, cryptox.ErrUnsupportedKey):
		return i18n.CertsErrorBadKey
	case errors.Is(err, cryptox.ErrBadCertificate):
		return i18n.CertsErrorBadCertificate
	}
	return i18n.CertsErrorNoCertificate
}

// decodeImport reads the content of a file: base64, as the UI sends any
// file, or else PEM text as it is.
func decodeImport(content string) []byte {
	if data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content)); err == nil {
		return data
	}
	return []byte(content)
}
