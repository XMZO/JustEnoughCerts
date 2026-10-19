// Package credentials holds the kinds of credential that modules share
// (providers.CredentialKind): the keys of an account at a cloud, which its
// deploy targets, its certificate sources and its DNS through lego take
// alike, and an SSH key that several servers accept. A type that takes one
// sets Type.Credential.
package credentials

import (
	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// The kinds, by vendor. Each type that fills its fields from one has
// fields of the same keys, or maps them (providers.CredentialUse).
var (
	Aliyun = providers.CredentialKind{
		ID: "aliyun", Name: i18n.CredentialsAliyunName, Description: i18n.CredentialsAliyunDescription, Hint: "access_key_id",
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.CredentialsAliyunAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.CredentialsAliyunAccessKeySecret, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	TencentCloud = providers.CredentialKind{
		ID: "tencentcloud", Name: i18n.CredentialsTencentcloudName, Description: i18n.CredentialsTencentcloudDescription, Hint: "secret_id",
		Fields: []providers.Field{
			{Key: "secret_id", Label: i18n.CredentialsTencentcloudSecretId, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.CredentialsTencentcloudSecretKey, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	Cloudflare = providers.CredentialKind{
		ID: "cloudflare", Name: i18n.CredentialsCloudflareName, Description: i18n.CredentialsCloudflareDescription,
		// An API token, or else the email of an account with its Global
		// API Key, which can do anything the account can.
		Fields: []providers.Field{
			{Key: "api_token", Label: i18n.CredentialsCloudflareApiToken, Kind: providers.Text, Secret: true},
			{Key: "api_email", Label: i18n.CredentialsCloudflareApiEmail, Kind: providers.Text},
			{Key: "api_key", Label: i18n.CredentialsCloudflareApiKey, Kind: providers.Text, Secret: true},
		},
	}
	AWS = providers.CredentialKind{
		ID: "aws", Name: i18n.CredentialsAwsName, Description: i18n.CredentialsAwsDescription, Hint: "access_key_id",
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.CredentialsAwsAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.CredentialsAwsSecretAccessKey, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	HuaweiCloud = providers.CredentialKind{
		ID: "huaweicloud", Name: i18n.CredentialsHuaweicloudName, Description: i18n.CredentialsHuaweicloudDescription, Hint: "access_key_id",
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.CredentialsHuaweicloudAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.CredentialsHuaweicloudSecretAccessKey, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	Volcengine = providers.CredentialKind{
		ID: "volcengine", Name: i18n.CredentialsVolcengineName, Description: i18n.CredentialsVolcengineDescription, Hint: "access_key_id",
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.CredentialsVolcengineAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.CredentialsVolcengineSecretAccessKey, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	BaiduCloud = providers.CredentialKind{
		ID: "baiducloud", Name: i18n.CredentialsBaiducloudName, Description: i18n.CredentialsBaiducloudDescription, Hint: "access_key",
		Fields: []providers.Field{
			{Key: "access_key", Label: i18n.CredentialsBaiducloudAccessKey, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.CredentialsBaiducloudSecretKey, Kind: providers.Text, Secret: true, Required: true},
		},
	}
	SSHKey = providers.CredentialKind{
		ID: "ssh_key", Name: i18n.CredentialsSshKeyName, Description: i18n.CredentialsSshKeyDescription,
		Fields: []providers.Field{
			{Key: "private_key", Label: i18n.CredentialsSshKeyPrivateKey, Kind: providers.Multiline, Secret: true, Required: true},
			{Key: "passphrase", Label: i18n.CredentialsSshKeyPassphrase, Kind: providers.Text, Secret: true},
		},
	}
)

// Use returns how a type takes a credential of kind, under the keys of
// its own fields that fields maps the kind's to, or under the same keys.
func Use(kind providers.CredentialKind, fields ...string) *providers.CredentialUse {
	u := &providers.CredentialUse{Kind: kind}
	if len(fields) > 0 {
		u.Fields = map[string]string{}
		for i, f := range kind.Fields {
			if i < len(fields) && fields[i] != f.Key {
				u.Fields[f.Key] = fields[i]
			}
		}
	}
	return u
}
