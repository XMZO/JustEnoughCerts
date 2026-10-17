package certs

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// CertificateBody is a certificate as the API returns it.
type CertificateBody struct {
	ID              int64            `json:"id"`
	Domains         []string         `json:"domains" doc:"The domains, the primary first"`
	Source          string           `json:"source" enum:"upload,provider" doc:"Where new versions come from: uploads only, or the source provider"`
	SourceProvider  int64            `json:"sourceProvider,omitempty" doc:"The source provider instance that issues the certificate"`
	SolverProvider  int64            `json:"solverProvider,omitempty" doc:"The solver provider instance that proves control of the domains"`
	KeyType         string           `json:"keyType" doc:"Type and size of the key, such as rsa-2048 or ecdsa-p256"`
	RenewBeforeDays int              `json:"renewBeforeDays" doc:"How many days before the end the certificate is renewed, or a reminder sent"`
	State           string           `json:"state" enum:"ok,expiring,expired,issuing,deploying,failed,deploy_failed" doc:"issuing: an issuance is under way; failed: the latest issuance failed or is waiting to retry; deploy_failed: the latest deployment to a target failed or is waiting to retry; deploying: a deployment is under way; otherwise by the current version: ok, expiring (within renewBeforeDays of the end) or expired"`
	Current         *VersionBody     `json:"current,omitempty" doc:"The version in use"`
	RenewAt         *time.Time       `json:"renewAt,omitempty" doc:"When the renewal of the current version starts, or started: for a certificate with a source"`
	Issuance        *IssuanceBody    `json:"issuance,omitempty" doc:"The latest issuance"`
	Targets         []int64          `json:"targets" doc:"The target provider instances each version is deployed to, in order"`
	Deployments     []DeploymentBody `json:"deployments" doc:"The latest deployment to each target that had one, in the order of targets"`
	CreatedAt       time.Time        `json:"createdAt"`
}

// VersionBody is a version as the API returns it.
type VersionBody struct {
	ID          int64     `json:"id"`
	Origin      string    `json:"origin" enum:"upload,issued" doc:"How the version came: uploaded, or issued by the source"`
	Names       []string  `json:"names" doc:"The names the certificate is valid for"`
	Serial      string    `json:"serial" doc:"Serial number, lowercase hex"`
	Fingerprint string    `json:"fingerprint" doc:"SHA-256 of the certificate, lowercase hex"`
	Issuer      string    `json:"issuer"`
	KeyType     string    `json:"keyType"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ChainEntry is a certificate of a chain.
type ChainEntry struct {
	Subject  string    `json:"subject"`
	Issuer   string    `json:"issuer"`
	NotAfter time.Time `json:"notAfter"`
}

func certificateBody(c *Certificate, now time.Time, sch schedule) CertificateBody {
	b := CertificateBody{
		ID: c.ID, Domains: c.Domains, Source: c.Source, SourceProvider: c.SourceProvider, SolverProvider: c.SolverProvider,
		KeyType: c.KeyType, RenewBeforeDays: c.RenewBeforeDays, State: c.state(now), CreatedAt: c.CreatedAt,
	}
	if c.Current != nil {
		v := versionBody(c.Current)
		b.Current = &v
		if c.Source == sourceProvider {
			at := sch.renewAt(c.Current.NotAfter, c.RenewBeforeDays)
			b.RenewAt = &at
		}
	}
	if c.Issuance != nil {
		is := issuanceBody(c.Issuance)
		b.Issuance = &is
	}
	b.Targets = append([]int64{}, c.Targets...)
	b.Deployments = make([]DeploymentBody, 0, len(c.Deployments))
	for _, d := range c.Deployments {
		b.Deployments = append(b.Deployments, deploymentBody(d))
	}
	return b
}

func versionBody(v *Version) VersionBody {
	return VersionBody{
		ID: v.ID, Origin: v.Origin, Names: v.Names, Serial: v.Serial, Fingerprint: v.Fingerprint,
		Issuer: v.Issuer, KeyType: v.KeyType, NotBefore: v.NotBefore, NotAfter: v.NotAfter, CreatedAt: v.CreatedAt,
	}
}

type idInput struct {
	ID int64 `path:"id"`
}

type listOutput struct {
	Body struct {
		Certificates []CertificateBody `json:"certificates" doc:"The soonest to expire first"`
	}
}

type certificateOutput struct {
	Body CertificateBody
}

type detailOutput struct {
	Body struct {
		Certificate CertificateBody `json:"certificate"`
		Versions    []VersionBody   `json:"versions" doc:"Every version, the newest first"`
		Chain       []ChainEntry    `json:"chain" doc:"The chain of the current version, the leaf first"`
	}
}

// PEMFields are the PEM texts of an upload.
type PEMFields struct {
	Certificate string `json:"certificate" minLength:"1" maxLength:"262144" doc:"PEM certificates: the leaf and its intermediates, in any order. The private key may come along."`
	PrivateKey  string `json:"privateKey,omitempty" maxLength:"65536" doc:"The PEM private key, unless it is in certificate"`
}

type addInput struct {
	Body struct {
		PEMFields
		RenewBeforeDays int     `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Days before the end to send a reminder; that of the settings if left out"`
		Targets         []int64 `json:"targets,omitempty" maxItems:"50" doc:"The target provider instances to deploy each version to, in order"`
	}
}

type uploadInput struct {
	ID   int64 `path:"id"`
	Body PEMFields
}

type downloadInput struct {
	ID      int64 `path:"id"`
	Version int64 `path:"version"`
	Body    struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"The password of the signed-in account, asked again before a private key leaves JEC"`
	}
}

type downloadOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	CacheControl       string `header:"Cache-Control"`
	Body               []byte
}

type updateInput struct {
	ID   int64 `path:"id"`
	Body struct {
		RenewBeforeDays int     `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Unchanged if left out"`
		Targets         []int64 `json:"targets,omitempty" maxItems:"50" doc:"Replaces the targets when given; an empty list removes them all"`
	}
}

func registerAPI(a huma.API, s *service, confirm func(context.Context, string) error) {
	registerIssueAPI(a, s)
	registerDeployAPI(a, s)
	registerHistoryAPI(a, s)
	registerSettingsAPI(a, s)

	huma.Register(a, huma.Operation{
		OperationID: "certs-list", Method: http.MethodGet, Path: "/certificates",
		Summary: "List the certificates",
	}, func(ctx context.Context, _ *struct{}) (*listOutput, error) {
		certs, err := s.list(ctx)
		if err != nil {
			return nil, err
		}
		sch, err := s.schedule(ctx)
		if err != nil {
			return nil, err
		}
		out := &listOutput{}
		out.Body.Certificates = make([]CertificateBody, 0, len(certs))
		now := s.now()
		for _, c := range certs {
			out.Body.Certificates = append(out.Body.Certificates, certificateBody(c, now, sch))
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-add", Method: http.MethodPost, Path: "/certificates", DefaultStatus: http.StatusCreated,
		Summary: "Add a certificate by uploading it with its private key",
	}, func(ctx context.Context, in *addInput) (*certificateOutput, error) {
		b, err := cryptox.ParseBundle([]byte(in.Body.Certificate), []byte(in.Body.PrivateKey))
		if err != nil {
			return nil, bundleError(err)
		}
		targets, err := s.validTargets(ctx, in.Body.Targets)
		if err != nil {
			return nil, err
		}
		id, err := s.add(ctx, b, in.Body.RenewBeforeDays, targets)
		if err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, id)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-get", Method: http.MethodGet, Path: "/certificates/{id}",
		Summary: "Get a certificate with its versions and chain",
	}, func(ctx context.Context, in *idInput) (*detailOutput, error) {
		c, err := s.load(ctx, s.db, in.ID)
		if err != nil {
			return nil, serviceError(err)
		}
		versions, err := s.versions(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		out := &detailOutput{}
		if out.Body.Certificate, err = s.body(ctx, c); err != nil {
			return nil, err
		}
		out.Body.Versions = make([]VersionBody, 0, len(versions))
		for _, v := range versions {
			out.Body.Versions = append(out.Body.Versions, versionBody(v))
		}
		out.Body.Chain = []ChainEntry{}
		if c.Current != nil {
			chain, err := cryptox.ParseCertificates([]byte(c.Current.Chain))
			if err != nil {
				return nil, err
			}
			for _, cert := range chain {
				out.Body.Chain = append(out.Body.Chain, ChainEntry{
					Subject: subjectName(cert), Issuer: issuerName(cert), NotAfter: cert.NotAfter.UTC(),
				})
			}
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-update", Method: http.MethodPatch, Path: "/certificates/{id}",
		Summary: "Change the renewal policy or the targets of a certificate",
	}, func(ctx context.Context, in *updateInput) (*certificateOutput, error) {
		var targets []int64
		if in.Body.Targets != nil {
			var err error
			if targets, err = s.validTargets(ctx, in.Body.Targets); err != nil {
				return nil, err
			}
			targets = append([]int64{}, targets...) // not nil: an empty list removes them all
		}
		if err := s.update(ctx, in.ID, in.Body.RenewBeforeDays, targets); err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-upload", Method: http.MethodPost, Path: "/certificates/{id}/versions", DefaultStatus: http.StatusCreated,
		Summary: "Upload a new version of a certificate",
	}, func(ctx context.Context, in *uploadInput) (*certificateOutput, error) {
		b, err := cryptox.ParseBundle([]byte(in.Body.Certificate), []byte(in.Body.PrivateKey))
		if err != nil {
			return nil, bundleError(err)
		}
		if err := s.upload(ctx, in.ID, b); err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-download", Method: http.MethodPost, Path: "/certificates/{id}/versions/{version}/download",
		Summary: "Download a version with its private key, as a ZIP of PEM files",
		Responses: map[string]*huma.Response{"200": {
			Description: "cert.pem, chain.pem (when there are intermediates), fullchain.pem and privkey.pem",
			Content:     map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *downloadInput) (*downloadOutput, error) {
		if err := confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		user, _ := auth.UserFrom(ctx)
		data, name, err := s.download(ctx, in.ID, in.Version, user.Username)
		if err != nil {
			return nil, serviceError(err)
		}
		return &downloadOutput{
			ContentType:        "application/zip",
			ContentDisposition: `attachment; filename="` + name + `"`,
			CacheControl:       "no-store",
			Body:               data,
		}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-delete", Method: http.MethodDelete, Path: "/certificates/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a certificate with all its versions",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		return nil, serviceError(s.remove(ctx, in.ID))
	})
}

func (s *service) output(ctx context.Context, id int64) (*certificateOutput, error) {
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, serviceError(err)
	}
	b, err := s.body(ctx, c)
	if err != nil {
		return nil, err
	}
	return &certificateOutput{Body: b}, nil
}

// body is certificateBody of c now, with the time its renewal starts.
func (s *service) body(ctx context.Context, c *Certificate) (CertificateBody, error) {
	sch, err := s.schedule(ctx)
	if err != nil {
		return CertificateBody{}, err
	}
	return certificateBody(c, s.now(), sch), nil
}

// bundleError turns what is wrong with an upload into a response that
// points at the field to fix.
func bundleError(err error) error {
	field, code := "body.certificate", i18n.CertsErrorBadCertificate
	for _, e := range []struct {
		err   error
		field string
		code  i18n.Key
	}{
		{cryptox.ErrNoCertificate, "body.certificate", i18n.CertsErrorNoCertificate},
		{cryptox.ErrBadCertificate, "body.certificate", i18n.CertsErrorBadCertificate},
		{cryptox.ErrKeyMismatch, "body.privateKey", i18n.CertsErrorKeyMismatch},
		{cryptox.ErrNoKey, "body.privateKey", i18n.CertsErrorNoKey},
		{cryptox.ErrBadKey, "body.privateKey", i18n.CertsErrorBadKey},
		{cryptox.ErrEncryptedKey, "body.privateKey", i18n.CertsErrorEncryptedKey},
		{cryptox.ErrSeveralKeys, "body.privateKey", i18n.CertsErrorSeveralKeys},
		{cryptox.ErrUnsupportedKey, "body.privateKey", i18n.CertsErrorUnsupportedKey},
	} {
		if errors.Is(err, e.err) {
			field, code = e.field, e.code
			break
		}
	}
	return api.NewError(http.StatusUnprocessableEntity, code, nil).WithField(field, code)
}

// serviceError turns the errors of the service into responses; others
// stay server errors.
func serviceError(err error) error {
	var dup *duplicateError
	var other *otherDomainsError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errNotFound):
		return api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	case errors.As(err, &dup):
		return api.NewError(http.StatusConflict, i18n.CertsErrorDuplicate, map[string]any{"certificateId": dup.certificate})
	case errors.As(err, &other):
		return api.NewError(http.StatusUnprocessableEntity, i18n.CertsErrorOtherDomains, map[string]any{"domain": other.domain}).
			WithField("body.certificate", i18n.CertsErrorOtherDomains)
	case errors.Is(err, errNotNewer):
		return api.NewError(http.StatusUnprocessableEntity, i18n.CertsErrorNotNewer, nil).
			WithField("body.certificate", i18n.CertsErrorNotNewer)
	case errors.Is(err, errNoSource):
		return api.NewError(http.StatusConflict, i18n.CertsErrorNoSource, nil)
	case errors.Is(err, errNoVersion):
		return api.NewError(http.StatusConflict, i18n.CertsErrorNoVersion, nil)
	}
	return err
}
