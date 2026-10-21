# Access keys and least privilege

[中文](permissions.zh-CN.md)

JEC needs only the API operations below. Give it the keys of a sub-account
(RAM user, CAM sub-user, IAM user, API token) allowed these operations and
nothing else, rather than the keys of the main account. JEC stores the keys
encrypted with its master key; see [Data and backups](../operations/backup.md).

The operations are those JEC calls, read from its code; a cloud may
require more for a resource it touches on the way, as noted. Types only
tested against a fake are marked Untested in the panel; see
[Providers and test coverage](providers.md).

## Alibaba Cloud (RAM)

A RAM policy allows actions as `<product>:<API name>`, such as
`cdn:SetCdnDomainSSLCertificate`.

| Use | Actions |
|---|---|
| ESA deploy target (`esa.site`) | `esa:ListSites`, `esa:ListCertificates`, `esa:SetCertificate`, `esa:DeleteCertificate` |
| ESA edge answers (`esa.edge`) | `esa:ListSites`, `esa:GetKvNamespace`, `esa:CreateKvNamespace`, `esa:PutKv`, `esa:DeleteKv`, `esa:GetRoutine`, `esa:CreateRoutine`, `esa:GetRoutineStagingCodeUploadInfo`, `esa:CommitRoutineStagingCode`, `esa:CreateRoutineCodeDeployment`, `esa:ListRoutineCodeVersions`, `esa:DeleteRoutineCodeVersion`, `esa:ListRoutineRoutes`, `esa:CreateRoutineRoute`, `esa:UpdateRoutineRoute` |
| CDN (`aliyun.cdn`) | `cdn:SetCdnDomainSSLCertificate`, `cdn:DescribeDomainCertificateInfo` |
| DCDN (`aliyun.dcdn`) | `dcdn:SetDcdnDomainSSLCertificate`, `dcdn:DescribeDcdnDomainCertificateInfo` |
| Certificate Management Service (`aliyun.cas`) | `yundun-cert:UploadUserCertificate`, `yundun-cert:DeleteUserCertificate`, `yundun-cert:ListUserCertificateOrder` |
| Certificates issued there (`aliyun.fetch`) | `yundun-cert:ListUserCertificateOrder`, `yundun-cert:GetUserCertificateDetail` |
| Alibaba Cloud DNS (validation) | `alidns:DescribeDomains`, `alidns:DescribeDomainRecords`, `alidns:AddDomainRecord`, `alidns:DeleteDomainRecord` |

The OSS, CLB and ALB targets upload to Certificate Management Service
first, so they need its actions as well as their own: `oss:PutCname` and
`oss:ListCname` for OSS; for CLB and ALB, the listener operations that
read and set certificates.

## Tencent Cloud (CAM)

A CAM policy allows actions as `<service>:<Action>`, such as
`ssl:UploadCertificate`.

| Use | Actions |
|---|---|
| SSL Certificates, updating resources (`tencentcloud.ssl`) | `ssl:UploadCertificate`, `ssl:UpdateCertificateInstance`, `ssl:DescribeCertificates`, `ssl:DeleteCertificate`; Tencent Cloud also checks the sub-user's permissions on the resources updated, such as CDN or CLB |
| CDN directly (`tencentcloud.cdn`) | `ssl:UploadCertificate`, `ssl:DeployCertificateInstance`, `ssl:DeleteCertificate`, `ssl:DescribeCertificates` |
| EdgeOne directly (`tencentcloud.teo`) | `ssl:UploadCertificate`, `ssl:DeleteCertificate`, `ssl:DescribeCertificates`, `teo:ModifyHostsCertificate` |
| Certificates issued there (`tencentcloud.fetch`) | `ssl:DescribeCertificates`, `ssl:DownloadCertificate` |
| Free certificates (`tencentcloud.free`) | `ssl:ApplyCertificate`, `ssl:DescribeCertificate`, `ssl:CompleteCertificate`, `ssl:DownloadCertificate`, and the DNSPod actions below for the records Tencent Cloud adds itself |
| DNSPod (validation) | `dnspod:DescribeDomainList`, `dnspod:DescribeRecordList`, `dnspod:CreateRecord`, `dnspod:DeleteRecord` |

## AWS (IAM)

| Use | Actions |
|---|---|
| Certificate Manager (`aws.acm`) | `acm:ImportCertificate`, `acm:AddTagsToCertificate`, `acm:DescribeCertificate`, `acm:ListCertificates` |
| CloudFront (`aws.cloudfront`) | The ACM actions above, in `us-east-1`, and `cloudfront:GetDistributionConfig`, `cloudfront:UpdateDistribution` |
| Route 53 (validation) | `route53:ListHostedZonesByName`, `route53:ListResourceRecordSets`, `route53:ChangeResourceRecordSets`, `route53:GetChange` |

## Cloudflare (API token)

| Use | Permissions |
|---|---|
| DNS validation | Zone → Zone → Read; Zone → DNS → Edit |
| Custom certificates (`cloudflare.custom`) | Zone → SSL and Certificates → Edit |
| Edge answers (`cloudflare.edge`) | As listed in [Domain validation](../usage/validation.md) |

Limit the token to the zones of the certificates, under Zone Resources.

## Others

Huawei Cloud's CDN target calls `ShowDomainFullConfig` and
`UpdateDomainFullConfig`, its ELB target `ListCertificates`,
`ShowCertificate`, `CreateCertificate`, `UpdateCertificate`,
`ShowListener` and `UpdateListener`; grant an IAM policy that allows these
operations. Panels and servers, such as BaoTa or SSH, take a key or an
account of their own; give them one made for JEC where they can.
