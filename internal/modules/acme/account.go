package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/lego"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// custom is the "ca" of an account at a CA lego does not know, whose
// directory URL comes with it.
const custom = "custom"

// cas are the CAs lego knows, by their lego codes, with the message key of
// their names.
var cas = []providers.Option{
	{Value: lego.CodeLetsEncrypt, Label: i18n.AcmeCaLetsencrypt},
	{Value: lego.CodeLetsEncryptStaging, Label: i18n.AcmeCaLetsencryptStaging},
	{Value: lego.CodeZeroSSL, Label: i18n.AcmeCaZerossl},
	{Value: lego.CodeGoogleTrust, Label: i18n.AcmeCaGoogletrust},
	{Value: lego.CodeGoogleTrustStaging, Label: i18n.AcmeCaGoogletrustStaging},
	{Value: lego.CodeActalis, Label: i18n.AcmeCaActalis},
	{Value: lego.CodeDigicert, Label: i18n.AcmeCaDigicert},
	{Value: lego.CodeFreeSSL, Label: i18n.AcmeCaFreessl},
	{Value: lego.CodeGlobalSign, Label: i18n.AcmeCaGlobalsign},
	{Value: lego.CodeLiteSSL, Label: i18n.AcmeCaLitessl},
	{Value: lego.CodePeeringHub, Label: i18n.AcmeCaPeeringhub},
	{Value: lego.CodeSSLComECC, Label: i18n.AcmeCaSslcomecc},
	{Value: lego.CodeSSLComRSA, Label: i18n.AcmeCaSslcomrsa},
	{Value: lego.CodeSectigo, Label: i18n.AcmeCaSectigo},
	{Value: lego.CodeSectigoEV, Label: i18n.AcmeCaSectigoev},
	{Value: lego.CodeSectigoOV, Label: i18n.AcmeCaSectigoov},
	{Value: custom, Label: i18n.AcmeCaCustom},
}

func accountType() providers.Type {
	return providers.Type{
		ID: "acme.account", Kind: providers.Source,
		Name: i18n.AcmeAccountName, Description: i18n.AcmeAccountDescription,
		Fields: []providers.Field{
			{Key: "ca", Label: i18n.AcmeFieldCa, Help: i18n.AcmeFieldCaHelp, Kind: providers.Select, Required: true,
				Default: lego.CodeLetsEncrypt, Options: cas},
			{Key: "directory", Label: i18n.AcmeFieldDirectory, Help: i18n.AcmeFieldDirectoryHelp, Kind: providers.URL, Required: true,
				When: &providers.When{Key: "ca", Value: custom}},
			{Key: "email", Label: i18n.AcmeFieldEmail, Help: i18n.AcmeFieldEmailHelp, Kind: providers.Email},
			{Key: "eab_kid", Label: i18n.AcmeFieldEabKid, Help: i18n.AcmeFieldEabKidHelp, Kind: providers.Text},
			{Key: "eab_hmac", Label: i18n.AcmeFieldEabHmac, Help: i18n.AcmeFieldEabHmacHelp, Kind: providers.Text, Secret: true},
			httpx.ProxyField(),
		},
		NeedsSolver: true, Challenges: []string{providers.ChallengeDNS, providers.ChallengeHTTP}, Testable: true, Revocable: true,
	}
}

// directoryOf returns the ACME directory URL of an account.
func directoryOf(in providers.Instance) (string, error) {
	ca := in.Config["ca"]
	if ca == custom {
		if dir := in.Config["directory"]; dir != "" {
			return dir, nil
		}
		return "", &contracts.Problem{Key: i18n.AcmeErrorDirectoryRequired}
	}
	return lego.GetDirectoryURL(ca)
}

// eabHMAC turns an EAB HMAC key into the base64url form lego takes, from
// standard base64 too, which some CAs show.
func eabHMAC(s string) string {
	s = strings.NewReplacer("+", "-", "/", "_").Replace(strings.TrimSpace(s))
	return strings.TrimRight(s, "=")
}

// account is the stored key and URL of the account of a provider instance
// at directory. A new key replaces the old one when the instance changed
// CA, since an account belongs to one CA.
func (m *Module) account(ctx context.Context, providerID int64, directory string) (crypto.Signer, string, error) {
	sealCtx := "account:" + strconv.FormatInt(providerID, 10)
	var dir, location string
	var sealed []byte
	err := m.db.QueryRowContext(ctx, "SELECT directory, location, sealed_key FROM acme_accounts WHERE provider_id = ?", providerID).
		Scan(&dir, &location, &sealed)
	switch {
	case err == nil && dir == directory:
		pemKey, err := m.secrets.Open(sealed, sealCtx)
		if err != nil {
			return nil, "", err
		}
		key, err := cryptox.ParseKey(pemKey)
		return key, location, err
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return nil, "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemKey, err := cryptox.EncodeKey(key)
	if err != nil {
		return nil, "", err
	}
	now := m.now().UnixMilli()
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO acme_accounts (provider_id, directory, location, sealed_key, created_at, updated_at) VALUES (?, ?, '', ?, ?, ?)
		 ON CONFLICT (provider_id) DO UPDATE SET directory = excluded.directory, location = '', sealed_key = excluded.sealed_key, updated_at = excluded.updated_at`,
		providerID, directory, m.secrets.Seal(pemKey, sealCtx), now, now)
	if err != nil {
		return nil, "", err
	}
	return key, "", nil
}

// registered stores the URL the CA gave the account.
func (m *Module) registered(ctx context.Context, providerID int64, location string) error {
	_, err := m.db.ExecContext(ctx, "UPDATE acme_accounts SET location = ?, updated_at = ? WHERE provider_id = ?",
		location, m.now().UnixMilli(), providerID)
	return err
}

// user is an account as lego sees it.
type user struct {
	email string
	reg   *legoacme.ExtendedAccount
	key   crypto.Signer
}

func (u *user) GetEmail() string                           { return u.email }
func (u *user) GetRegistration() *legoacme.ExtendedAccount { return u.reg }
func (u *user) GetPrivateKey() crypto.Signer               { return u.key }
