package tencentcloud

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The certificates SSL Certificates issued, bought or free ones, are taken
// in with the keys Tencent Cloud made for them, out of the ZIP archive the
// console downloads too: a source of certificates with keys of their own
// (contracts.KeySource). Renewing a certificate there, or applying for the
// next free one, gives JEC the next one to take in; those uploaded there
// are left alone, as JEC's own.

// recheck is how long a certificate due for renewal waits before the
// service is looked at again for the next one.
const recheck = 6 * time.Hour

// Bounds of the archive JEC reads: how many files, and how big each.
const (
	archiveFiles = 100
	archiveSize  = 1 << 20
)

func fetchType() providers.Type {
	return providers.Type{
		ID: "tencentcloud.fetch", Kind: providers.Source, OwnKey: true, Holds: true, Name: i18n.TencentcloudFetchName, Description: i18n.TencentcloudFetchDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "secret_id", Label: i18n.TencentcloudFieldSecretId, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.TencentcloudFieldSecretKey, Help: i18n.TencentcloudFieldFetchSecretKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "site", Label: i18n.TencentcloudFieldSite, Kind: providers.Select, Default: siteChina, Advanced: true, Options: []providers.Option{
				{Value: siteChina, Label: i18n.TencentcloudSiteChina},
				{Value: siteIntl, Label: i18n.TencentcloudSiteInternational},
			}},
		},
		Credential: credentials.Use(credentials.TencentCloud),
	}
}

// fetcher takes in the certificates SSL Certificates issued to an
// account.
type fetcher struct {
	api      sslAPI
	endpoint string
}

var (
	_ contracts.KeySource = (*fetcher)(nil)
	_ contracts.Holder    = (*fetcher)(nil)
	_ contracts.Tester    = (*fetcher)(nil)
)

func (m *Module) fetchFactory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := endpointChina
	if in.Config["site"] == siteIntl {
		endpoint = endpointIntl
	}
	api, err := m.open(strings.TrimSpace(in.Value("secret_id")), strings.TrimSpace(in.Value("secret_key")), endpoint)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return &fetcher{api: api, endpoint: endpoint}, nil
}

// Close has nothing to release.
func (*fetcher) Close() error { return nil }

// Test counts the certificates of the account, which needs the keys to
// work.
func (f *fetcher) Test(ctx context.Context) (contracts.Message, error) {
	n, err := f.api.count(ctx)
	if err != nil {
		return contracts.Message{}, explain(err, f.endpoint)
	}
	return contracts.Message{Key: i18n.TencentcloudTestFetchOk, Params: map[string]any{"count": n}}, nil
}

// beijing is the time zone of the times the service tells.
var beijing = time.FixedZone("CST", 8*60*60)

// Held lists the certificates the service issued that JEC can take in.
func (f *fetcher) Held(ctx context.Context) ([]contracts.Held, error) {
	list, err := f.api.issued(ctx, "")
	if err != nil {
		return nil, explain(err, f.endpoint)
	}
	var held []contracts.Held
	for _, c := range list {
		if !c.Download {
			continue
		}
		end, _ := time.ParseInLocation(time.DateTime, c.End, beijing)
		held = append(held, contracts.Held{Domains: c.Names, NotAfter: end})
	}
	return held, nil
}

// Collect takes in the newest certificate the service issued for the
// domains, unless it is the one in use or older: then the certificate
// waits for the next one.
func (f *fetcher) Collect(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	domains := strings.Join(req.Domains, ", ")
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.TencentcloudLogFetchLook, Params: map[string]any{"domains": domains}})
	list, err := f.api.issued(ctx, strings.TrimPrefix(cmp.Or(req.Domains...), "*."))
	if err != nil {
		return nil, explain(err, f.endpoint)
	}
	var best *issuedCert
	for _, c := range list {
		if c.Download && cryptox.Covers(c.Names, req.Domains) && (best == nil || c.End > best.End) {
			best = &c
		}
	}
	if best == nil {
		return nil, &contracts.Problem{Key: i18n.TencentcloudErrorFetchNone, Params: map[string]any{"domains": domains}}
	}
	data, err := f.api.download(ctx, best.ID)
	if err != nil {
		return nil, explain(err, f.endpoint)
	}
	b, err := bundleOf(data, req.Domains)
	switch {
	case errors.Is(err, cryptox.ErrNoKey):
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFetchNoKey, Params: map[string]any{"id": best.ID}})
	case err != nil:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFetchBad, Params: map[string]any{"id": best.ID}, Err: err})
	}
	if req.Current != "" && (cryptox.Fingerprint(b.Leaf) == req.Current || b.Leaf.NotAfter.Before(req.CurrentEnd)) {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.TencentcloudLogFetchSame,
			Params: map[string]any{"domains": domains, "id": best.ID, "hours": int(recheck.Hours())}})
		return nil, &contracts.Wait{After: recheck}
	}
	if len(b.Chain) == 0 {
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepDownload, Key: i18n.TencentcloudLogFetchNoChain, Params: map[string]any{"id": best.ID}})
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.TencentcloudLogFetchFound,
		Params: map[string]any{"id": best.ID, "date": b.Leaf.NotAfter.Format(time.DateOnly)}})
	return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
}

// bundleOf reads the certificate of domains and its key out of the ZIP
// archive the console downloads, which has a folder for each kind of web
// server: every PEM file in it, as the same key and certificates come in
// several. A key that is not there, as for a CSR of one's own, is
// cryptox.ErrNoKey.
func bundleOf(data []byte, domains []string) (*cryptox.Bundle, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var certs []*x509.Certificate
	var keys []crypto.Signer
	encrypted := false
	for i, f := range r.File {
		if i >= archiveFiles {
			break
		}
		switch strings.ToLower(path.Ext(f.Name)) {
		case ".crt", ".pem", ".cer", ".key":
		default:
			continue // the formats of other servers, such as JKS and PFX
		}
		b, err := read(f)
		if err != nil {
			return nil, err
		}
		if found, err := cryptox.ParseCertificates(b); err == nil {
			for _, c := range found {
				if !slices.ContainsFunc(certs, c.Equal) {
					certs = append(certs, c)
				}
			}
		}
		k, err := cryptox.ParseKey(b)
		switch {
		case err == nil:
			keys = append(keys, k)
		case errors.Is(err, cryptox.ErrEncryptedKey):
			encrypted = true
		}
	}
	if len(keys) == 0 && encrypted {
		return nil, cryptox.ErrEncryptedKey
	}
	if len(keys) == 0 {
		return nil, cryptox.ErrNoKey
	}
	var best *cryptox.Bundle
	for _, k := range keys {
		b, err := cryptox.NewBundle(certs, k)
		if err == nil && cryptox.Covers(cryptox.Names(b.Leaf), domains) && (best == nil || b.Leaf.NotAfter.After(best.Leaf.NotAfter)) {
			best = b
		}
	}
	if best == nil {
		return nil, cryptox.ErrKeyMismatch
	}
	return best, nil
}

// read returns the content of a file of the archive, of archiveSize at
// most.
func read(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > archiveSize {
		return nil, errors.New("tencentcloud: a file of the archive is too big")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, archiveSize))
}
