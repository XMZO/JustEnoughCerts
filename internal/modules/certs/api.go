package certs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// CertificateBody is a certificate as the API returns it.
type CertificateBody struct {
	ID              int64            `json:"id"`
	Domains         []string         `json:"domains" doc:"The domains, the primary first"`
	Source          string           `json:"source" enum:"upload,provider" doc:"Where new versions come from: uploads only, or the source provider"`
	SourceProvider  int64            `json:"sourceProvider,omitempty" doc:"The source provider instance that issues the certificate"`
	BackupProvider  int64            `json:"backupProvider,omitempty" doc:"The source that tries when an attempt with the certificate's own failed"`
	SolverProvider  int64            `json:"solverProvider,omitempty" doc:"The solver provider instance that proves control of the domains"`
	KeyType         string           `json:"keyType" doc:"Type and size of the key, such as rsa-2048 or ecdsa-p256"`
	Note            string           `json:"note,omitempty" doc:"What people wrote about it"`
	Reminder        *ReminderBody    `json:"reminder,omitempty" doc:"A day someone set to be reminded on, as to renew a subscription bought"`
	ReuseKey        bool             `json:"reuseKey,omitempty" doc:"Renewals keep the private key of the version in use"`
	Paused          bool             `json:"paused,omitempty" doc:"The renewal is paused: the certificate is issued only when someone asks"`
	DeployWindow    string           `json:"deployWindow,omitempty" doc:"The hours, in the panel's time zone, new versions are deployed in, as 1-5"`
	NotifyTo        []int64          `json:"notifyTo" doc:"The notifiers its notes go to; empty for all"`
	RenewBeforeDays int              `json:"renewBeforeDays" doc:"How many days before the end the certificate is renewed, or a reminder sent"`
	State           string           `json:"state" enum:"ok,expiring,expired,issuing,deploying,failed,deploy_failed,revoked,awaiting" doc:"issuing: an issuance is under way; failed: the latest issuance failed or is waiting to retry; deploy_failed: the latest deployment to a target failed or is waiting to retry; deploying: a deployment is under way; awaiting: no version yet, and a CSR waits for its certificate; otherwise by the current version: ok, expiring (within renewBeforeDays of the end), expired, or revoked (its CA revoked it)"`
	Current         *VersionBody     `json:"current,omitempty" doc:"The version in use"`
	RenewAt         *time.Time       `json:"renewAt,omitempty" doc:"When the renewal of the current version starts, or started: for a certificate with a source"`
	RenewalWindow   *RenewalBody     `json:"renewalWindow,omitempty" doc:"The window the CA suggests renewing the current version in (ACME Renewal Information), once it answered"`
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
	// Revocable, RevokedAt and RevokeReason are only in the versions of a
	// certificate's details.
	Revocable    bool       `json:"revocable,omitempty" doc:"JEC can have the version revoked at its CA now"`
	Usable       bool       `json:"usable,omitempty" doc:"The version can become current again: it has not expired or been revoked, and is for the certificate's names"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty" doc:"When JEC had the version revoked"`
	RevokeReason string     `json:"revokeReason,omitempty" enum:"unspecified,key_compromise,superseded,cessation_of_operation"`
	// CARevokedAt and CARevokeReason are in every version.
	CARevokedAt    *time.Time `json:"caRevokedAt,omitempty" doc:"When its CA revoked the version, as its OCSP responder or CRL told"`
	CARevokeReason string     `json:"caRevokeReason,omitempty" enum:"unspecified,key_compromise,ca_compromise,affiliation_changed,superseded,cessation_of_operation,certificate_hold,privilege_withdrawn" doc:"Why, as RFC 5280 names it"`
}

// caReasons names the reason codes of RFC 5280.
var caReasons = map[int]string{
	1: "key_compromise", 2: "ca_compromise", 3: "affiliation_changed", 4: "superseded", 5: "cessation_of_operation",
	6: "certificate_hold", 9: "privilege_withdrawn", 10: "ca_compromise",
}

// RenewalBody is the window a CA suggests renewing a version in.
type RenewalBody struct {
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	ExplanationURL string    `json:"explanationUrl,omitempty" doc:"A page of the CA that says why, as before a revocation"`
}

// ChainEntry is a certificate of a chain.
type ChainEntry struct {
	Subject  string    `json:"subject"`
	Issuer   string    `json:"issuer"`
	NotAfter time.Time `json:"notAfter"`
}

// DetailsBody is what a version's certificate says beyond its names and
// dates (cryptox.Details).
type DetailsBody struct {
	Subject            string   `json:"subject" doc:"The distinguished name, as RFC 2253 writes it"`
	Issuer             string   `json:"issuer" doc:"The issuer's distinguished name"`
	SignatureAlgorithm string   `json:"signatureAlgorithm" doc:"As Go names it: SHA256-RSA, ECDSA-SHA384"`
	SHA1               string   `json:"sha1" doc:"The thumbprint, as Windows shows it, in hex"`
	SPKISHA256         string   `json:"spkiSha256" doc:"The SHA-256 of the public key, in base64, as pins are"`
	KeyUsage           []string `json:"keyUsage" enum:"digital_signature,content_commitment,key_encipherment,data_encipherment,key_agreement,cert_sign,crl_sign,encipher_only,decipher_only"`
	ExtKeyUsage        []string `json:"extKeyUsage" enum:"any,server_auth,client_auth,code_signing,email_protection,time_stamping,ocsp_signing"`
	OCSP               []string `json:"ocsp" doc:"Where its revocation is looked up, by OCSP"`
	CRL                []string `json:"crl" doc:"The lists of revoked certificates of its issuer"`
	CAIssuers          []string `json:"caIssuers" doc:"Where its issuer's certificate is"`
	Validation         string   `json:"validation,omitempty" enum:"dv,ov,iv,ev" doc:"What the CA checked, by the policy of the CA/Browser Forum it names"`
	SCTs               int      `json:"scts" doc:"The timestamps of Certificate Transparency logs it carries"`
	MustStaple         bool     `json:"mustStaple" doc:"Clients must get its OCSP status from the server"`
}

func detailsBody(d cryptox.Details) DetailsBody {
	return DetailsBody{
		Subject: d.Subject, Issuer: d.Issuer, SignatureAlgorithm: d.SignatureAlgorithm, SHA1: d.SHA1, SPKISHA256: d.SPKISHA256,
		KeyUsage: d.KeyUsage, ExtKeyUsage: d.ExtKeyUsage, OCSP: d.OCSP, CRL: d.CRL, CAIssuers: d.CAIssuers,
		Validation: d.Validation, SCTs: d.SCTs, MustStaple: d.MustStaple,
	}
}

// ReminderBody is a day to be reminded on, and what of.
type ReminderBody struct {
	On   string `json:"on" maxLength:"10" doc:"The day, as 2027-05-01, in the panel's time zone; the reminder goes out at 9:00. Empty for none"`
	Note string `json:"note,omitempty" maxLength:"200" doc:"What to be reminded of"`
	Sent bool   `json:"sent,omitempty" readOnly:"true" doc:"It went out"`
}

// CSRBody is a certificate request of a version's key for its names.
type CSRBody struct {
	CSR                string   `json:"csr" doc:"The request, in PEM form"`
	Subject            string   `json:"subject"`
	Names              []string `json:"names"`
	KeyType            string   `json:"keyType"`
	SignatureAlgorithm string   `json:"signatureAlgorithm"`
}

func certificateBody(c *Certificate, now time.Time, sch schedule) CertificateBody {
	b := CertificateBody{
		ID: c.ID, Domains: c.Domains, Source: c.Source, SourceProvider: c.SourceProvider, BackupProvider: c.BackupProvider, SolverProvider: c.SolverProvider,
		KeyType: c.KeyType, RenewBeforeDays: c.RenewBeforeDays, State: c.state(now), CreatedAt: c.CreatedAt, Note: c.Note, ReuseKey: c.ReuseKey, NotifyTo: c.NotifyTo,
		Paused: c.Paused, DeployWindow: c.DeployWindow,
	}
	if c.ReminderOn != "" {
		b.Reminder = &ReminderBody{On: c.ReminderOn, Note: c.ReminderNote, Sent: c.ReminderSent}
	}
	if c.Current != nil {
		v := versionBody(c.Current)
		b.Current = &v
		if c.Source == sourceProvider && !c.Paused {
			at := sch.due(c)
			b.RenewAt = &at
		}
		if r := c.Current.Renewal; r != nil && c.Source == sourceProvider {
			b.RenewalWindow = &RenewalBody{Start: r.Start, End: r.End, ExplanationURL: r.Explanation}
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
	b := VersionBody{
		ID: v.ID, Origin: v.Origin, Names: v.Names, Serial: v.Serial, Fingerprint: v.Fingerprint,
		Issuer: v.Issuer, KeyType: v.KeyType, NotBefore: v.NotBefore, NotAfter: v.NotAfter, CreatedAt: v.CreatedAt,
	}
	if !v.RevokedAt.IsZero() {
		at := v.RevokedAt
		b.RevokedAt, b.RevokeReason = &at, v.RevokeReason
	}
	if !v.CARevokedAt.IsZero() {
		at, reason := v.CARevokedAt, caReasons[v.CARevokeReason]
		if reason == "" {
			reason = "unspecified"
		}
		b.CARevokedAt, b.CARevokeReason = &at, reason
	}
	return b
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
		Details     *DetailsBody    `json:"details,omitempty" doc:"What the current version says beyond its names and dates"`
		Request     *RequestBody    `json:"request,omitempty" doc:"The request the certificate waits on: the certificate the CA issues for it is uploaded without a key"`
	}
}

type versionInput struct {
	ID      int64 `path:"id"`
	Version int64 `path:"version"`
}

type csrOutput struct {
	Body CSRBody
}

// UploadFields are what an upload carries: PEM texts, or a PKCS #12 file.
type UploadFields struct {
	Certificate string `json:"certificate,omitempty" maxLength:"262144" doc:"PEM certificates: the leaf and its intermediates, in any order. The private key may come along. Left out with pfx."`
	PrivateKey  string `json:"privateKey,omitempty" maxLength:"65536" doc:"The PEM private key, unless it is in certificate"`
	PFX         []byte `json:"pfx,omitempty" maxLength:"131072" doc:"A PKCS #12 file (.pfx, .p12) with the certificate, its chain and its private key, in place of certificate and privateKey"`
	PFXPassword string `json:"pfxPassword,omitempty" maxLength:"1024" doc:"The password of pfx, if it has one"`
}

type addInput struct {
	Body struct {
		UploadFields
		RenewBeforeDays int     `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Days before the end to send a reminder; that of the settings if left out"`
		Targets         []int64 `json:"targets,omitempty" maxItems:"50" doc:"The target provider instances to deploy each version to, in order"`
	}
}

type uploadInput struct {
	ID   int64 `path:"id"`
	Body UploadFields
}

type downloadInput struct {
	ID      int64 `path:"id"`
	Version int64 `path:"version"`
	Body    struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"The password of the signed-in account, asked again before a private key leaves JEC"`
		// PFX adds certificate.pfx, for Windows, IIS, Azure and Java.
		PFX         bool   `json:"pfx,omitempty" doc:"Add certificate.pfx, the certificate, its chain and its key as PKCS #12"`
		PFXPassword string `json:"pfxPassword,omitempty" maxLength:"128" doc:"The password of certificate.pfx; empty leaves it unencrypted"`
		PFXLegacy   bool   `json:"pfxLegacy,omitempty" doc:"Encrypt certificate.pfx with 3DES, as Windows Server 2016 and Java 8 need, rather than AES-256"`
	}
}

type exportInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"The password of the signed-in account, asked again before private keys leave JEC"`
	}
}

type revokeInput struct {
	ID      int64 `path:"id"`
	Version int64 `path:"version"`
	Body    struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"The password of the signed-in account, asked again before revoking"`
		Reason   string `json:"reason" enum:"unspecified,key_compromise,superseded,cessation_of_operation" doc:"Why, as RFC 5280 names it"`
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
		RenewBeforeDays int           `json:"renewBeforeDays,omitempty" minimum:"1" maximum:"365" doc:"Unchanged if left out"`
		Targets         []int64       `json:"targets,omitempty" maxItems:"50" doc:"Replaces the targets when given; an empty list removes them all"`
		Note            *string       `json:"note,omitempty" maxLength:"500" doc:"Replaces the note when given; empty removes it"`
		Reminder        *ReminderBody `json:"reminder,omitempty" doc:"Replaces the reminder when given; an empty day removes it"`
		ReuseKey        *bool         `json:"reuseKey,omitempty" doc:"Keep the private key of the version in use for renewals, as a pinned key or a TLSA record needs; unchanged if left out"`
		NotifyTo        *[]int64      `json:"notifyTo,omitempty" maxItems:"50" doc:"The notifiers the certificate's notes go to; empty for all; unchanged if left out"`
		Paused          *bool         `json:"paused,omitempty" doc:"Pause the renewal: the certificate is issued only when someone asks; unchanged if left out"`
		SolverProvider  *int64        `json:"solverProvider,omitempty" doc:"Another solver for a certificate with a source that needs one, from its next issuance; unchanged if left out"`
		BackupProvider  *int64        `json:"backupProvider,omitempty" doc:"The source, of a type marked backup, that tries when an attempt with the certificate's own failed; 0 for none; unchanged if left out"`
		KeyType         *string       `json:"keyType,omitempty" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096" doc:"Another key type for a certificate with a source whose keys JEC makes, from its next issuance; unchanged if left out"`
		DeployWindow    *string       `json:"deployWindow,omitempty" enum:",0-6,1-5,2-6,22-6" doc:"The hours, in the panel's time zone, new versions are deployed in, as 1-5; empty for any time; unchanged if left out. Deploying by hand starts at once."`
	}
}

func registerAPI(a huma.API, s *service, confirm func(context.Context, string) error) {
	registerIssueAPI(a, s)
	registerDeployAPI(a, s)
	registerHistoryAPI(a, s)
	registerSettingsAPI(a, s)
	registerImportAPI(a, s)
	registerKeysAPI(a, s)
	registerPullAPI(a, s, confirm)
	registerCSRAPI(a, s)

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
		b, err := readUpload(in.Body.UploadFields)
		if errors.Is(err, cryptox.ErrNoKey) && len(in.Body.PFX) == 0 {
			// Issued for a request JEC made: a version of its certificate.
			id, req, rerr := s.requestFor(ctx, in.Body.Certificate)
			if rerr != nil {
				return nil, rerr
			}
			if id != 0 {
				if err := s.upload(ctx, id, req); err != nil {
					return nil, serviceError(err)
				}
				return s.output(ctx, id)
			}
		}
		if err != nil {
			return nil, bundleError(err, len(in.Body.PFX) > 0)
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
		typeOf := s.typeCache(ctx)
		for _, v := range versions {
			b := versionBody(v)
			b.Revocable = revocable(c, v, s.now(), typeOf) == nil
			b.Usable = usable(c, v, s.now()) == nil
			out.Body.Versions = append(out.Body.Versions, b)
		}
		if out.Body.Request, err = s.pendingCSR(ctx, in.ID); err != nil {
			return nil, err
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
			if len(chain) > 0 {
				d := detailsBody(cryptox.Describe(chain[0]))
				out.Body.Details = &d
			}
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-csr", Method: http.MethodGet, Path: "/certificates/{id}/versions/{version}/csr",
		Summary:     "A certificate request of a version's key for its names",
		Description: "Made anew each time from the version's private key, which stays on the server: for asking another CA for a certificate of the same key and names.",
	}, func(ctx context.Context, in *versionInput) (*csrOutput, error) {
		pem, err := s.csr(ctx, in.ID, in.Version)
		if err != nil {
			return nil, serviceError(err)
		}
		r, err := cryptox.DescribeCSR(pem)
		if err != nil {
			return nil, err
		}
		return &csrOutput{Body: CSRBody{CSR: string(pem), Subject: r.Subject, Names: r.Names, KeyType: r.KeyType, SignatureAlgorithm: r.SignatureAlgorithm}}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-update", Method: http.MethodPatch, Path: "/certificates/{id}",
		Summary: "Change the renewal policy, the targets or the note of a certificate",
	}, func(ctx context.Context, in *updateInput) (*certificateOutput, error) {
		var targets []int64
		if in.Body.Targets != nil {
			var err error
			if targets, err = s.validTargets(ctx, in.Body.Targets); err != nil {
				return nil, err
			}
			targets = append([]int64{}, targets...) // not nil: an empty list removes them all
		}
		var notifyTo *[]int64
		if in.Body.NotifyTo != nil {
			list := []int64{}
			invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
			for i, id := range *in.Body.NotifyTo {
				if code := s.checkProvider(ctx, id, providers.Notifier); code != "" {
					invalid = invalid.WithField(fmt.Sprintf("body.notifyTo[%d]", i), code)
				} else if !slices.Contains(list, id) {
					list = append(list, id)
				}
			}
			if len(invalid.Fields) > 0 {
				return nil, invalid
			}
			notifyTo = &list
		}
		if err := s.checkIssuing(ctx, in.ID, in.Body.SolverProvider, in.Body.BackupProvider, in.Body.KeyType); err != nil {
			return nil, err
		}
		var rem *reminder
		if r := in.Body.Reminder; r != nil {
			if _, err := time.Parse(time.DateOnly, r.On); r.On != "" && err != nil {
				return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("body.reminder.on", i18n.KernelErrorInvalidField)
			}
			rem = &reminder{on: r.On, note: r.Note}
		}
		ch := changes{days: in.Body.RenewBeforeDays, targets: targets, note: in.Body.Note, reuseKey: in.Body.ReuseKey, paused: in.Body.Paused, notifyTo: notifyTo, reminder: rem,
			solver: in.Body.SolverProvider, backup: in.Body.BackupProvider, keyType: in.Body.KeyType, deployWindow: in.Body.DeployWindow}
		if err := s.update(ctx, in.ID, ch); err != nil {
			return nil, serviceError(err)
		}
		return s.output(ctx, in.ID)
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-upload", Method: http.MethodPost, Path: "/certificates/{id}/versions", DefaultStatus: http.StatusCreated,
		Summary: "Upload a new version of a certificate",
	}, func(ctx context.Context, in *uploadInput) (*certificateOutput, error) {
		b, err := readUpload(in.Body)
		if err != nil {
			// Without a key: that of its request, or of its version in use.
			if b, err = s.keyFor(ctx, in.ID, in.Body, err); err != nil {
				return nil, bundleError(err, len(in.Body.PFX) > 0)
			}
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
			Description: "cert.pem, chain.pem (when there are intermediates), fullchain.pem and privkey.pem, and certificate.pfx when asked for",
			Content:     map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *downloadInput) (*downloadOutput, error) {
		s.named(ctx, in.ID)
		if err := confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		user, _ := auth.UserFrom(ctx)
		var pfx *pfxOptions
		if in.Body.PFX {
			pfx = &pfxOptions{password: in.Body.PFXPassword, legacy: in.Body.PFXLegacy}
		}
		data, name, err := s.download(ctx, in.ID, in.Version, user.Username, pfx)
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
		OperationID: "certs-export", Method: http.MethodPost, Path: "/certificates/export",
		Summary: "Download the version in use of every certificate with its private key, as one ZIP",
		Responses: map[string]*huma.Response{"200": {
			Description: "A folder for each certificate, named after its primary domain and end, with the files of a download of it",
			Content:     map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
		}},
	}, func(ctx context.Context, in *exportInput) (*downloadOutput, error) {
		if err := confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		user, _ := auth.UserFrom(ctx)
		data, err := s.exportAll(ctx, user.Username)
		if err != nil {
			return nil, err
		}
		return &downloadOutput{
			ContentType:        "application/zip",
			ContentDisposition: `attachment; filename="jec-certificates_` + s.now().UTC().Format(time.DateOnly) + `.zip"`,
			CacheControl:       "no-store",
			Body:               data,
		}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-revoke", Method: http.MethodPost, Path: "/certificates/{id}/versions/{version}/revoke",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Have a version revoked at its CA, once the password is confirmed",
		Description: "Only a version that a source which revokes, such as an ACME account, issued, which has not expired " +
			"and is not the version in use: a new version replaces that one first.",
	}, func(ctx context.Context, in *revokeInput) (*struct{}, error) {
		s.named(ctx, in.ID)
		if err := confirm(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		if err := s.revoke(ctx, in.ID, in.Version, in.Body.Reason); err != nil {
			return nil, revokeError(err)
		}
		return nil, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-use-version", Method: http.MethodPost, Path: "/certificates/{id}/versions/{version}/use",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Go back to an earlier version, which is deployed to every target",
		Description:   "Only a version that has not expired or been revoked, for the certificate's names. The renewal goes by it from then on.",
	}, func(ctx context.Context, in *versionInput) (*struct{}, error) {
		s.named(ctx, in.ID)
		switch err := s.useVersion(ctx, in.ID, in.Version); {
		case errors.Is(err, errUseCurrent):
			return nil, api.NewError(http.StatusConflict, i18n.CertsErrorUseCurrent, nil)
		case errors.Is(err, errUseEnded):
			return nil, api.NewError(http.StatusConflict, i18n.CertsErrorUseEnded, nil)
		case errors.Is(err, errUseNames):
			return nil, api.NewError(http.StatusConflict, i18n.CertsErrorUseNames, nil)
		case err != nil:
			return nil, serviceError(err)
		}
		return nil, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-delete", Method: http.MethodDelete, Path: "/certificates/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a certificate with all its versions",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		s.named(ctx, in.ID)
		return nil, serviceError(s.remove(ctx, in.ID))
	})
}

// named gives the audit log the main domain of certificate id, as an
// operation on it does that answers with no certificate.
func (s *service) named(ctx context.Context, id int64) {
	if c, err := s.load(ctx, s.db, id); err == nil && len(c.Domains) > 0 {
		api.Subject(ctx, c.Domains[0])
	}
}

func (s *service) output(ctx context.Context, id int64) (*certificateOutput, error) {
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, serviceError(err)
	}
	if len(c.Domains) > 0 {
		api.Subject(ctx, c.Domains[0])
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

// readUpload reads the certificate and key of an upload: a PKCS #12 file
// when there is one, or else the PEM texts. Its errors are cryptox's,
// which bundleError turns into a response.
func readUpload(f UploadFields) (*cryptox.Bundle, error) {
	if len(f.PFX) == 0 {
		return cryptox.ParseBundle([]byte(f.Certificate), []byte(f.PrivateKey))
	}
	return cryptox.ParsePFX(f.PFX, f.PFXPassword)
}

// bundleError turns what is wrong with an upload into a response that
// points at the field to fix: those of the file, for a PKCS #12 one.
func bundleError(err error, pfx bool) error {
	field, code := "body.certificate", i18n.CertsErrorBadCertificate
	for _, e := range []struct {
		err   error
		field string
		code  i18n.Key
	}{
		{cryptox.ErrPFXPassword, "body.pfxPassword", i18n.CertsErrorPfxPassword},
		{cryptox.ErrBadPFX, "body.pfx", i18n.CertsErrorBadPfx},
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
	if pfx && field != "body.pfxPassword" {
		field = "body.pfx"
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
	case errors.Is(err, errIssuing):
		return api.NewError(http.StatusConflict, i18n.CertsErrorIssuingNow, nil)
	case errors.Is(err, errNoVersion):
		return api.NewError(http.StatusConflict, i18n.CertsErrorNoVersion, nil)
	}
	return err
}

// revokeError turns an error of revoking into a response.
func revokeError(err error) error {
	var problem *contracts.Problem
	switch {
	case errors.Is(err, errRevokeCurrent):
		return api.NewError(http.StatusConflict, i18n.CertsErrorRevokeCurrent, nil)
	case errors.Is(err, errRevokeSource):
		return api.NewError(http.StatusConflict, i18n.CertsErrorRevokeSource, nil)
	case errors.Is(err, errRevokeExpired):
		return api.NewError(http.StatusConflict, i18n.CertsErrorRevokeExpired, nil)
	case errors.Is(err, errRevoked):
		return api.NewError(http.StatusConflict, i18n.CertsErrorRevoked, nil)
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, providers.ErrUnavailable):
		return api.NewError(http.StatusConflict, i18n.CertsErrorRevokeSourceGone, nil)
	case errors.As(err, &problem):
		// What the CA or the source said.
		return api.NewError(http.StatusBadGateway, problem.Key, problem.Params)
	}
	return serviceError(err)
}
