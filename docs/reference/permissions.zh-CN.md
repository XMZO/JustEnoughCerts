# 密钥与最小权限

[English](permissions.md)

JEC 只需要下列 API 操作。请为 JEC 创建子账号（RAM 用户、CAM 子用户、IAM 用户或 API 令牌），只授予这些操作的权限，不要使用主账号的密钥。JEC 用主密钥加密保存这些密钥，参见[数据与备份](../operations/backup.zh-CN.md)。

下列操作取自 JEC 代码中实际调用的接口；云平台可能还会检查沿途涉及的其他资源的权限，见表中说明。只用替身测过的类型在面板中标有“未实测”，参见[服务商支持与测试情况](providers.zh-CN.md)。

## 阿里云（RAM）

RAM 策略中的操作写作 `<产品>:<API 名称>`，如 `cdn:SetCdnDomainSSLCertificate`。

| 用途 | 操作 |
|---|---|
| ESA 部署目标（`esa.site`） | `esa:ListSites`、`esa:ListCertificates`、`esa:SetCertificate`、`esa:DeleteCertificate` |
| ESA 边缘应答（`esa.edge`） | `esa:ListSites`、`esa:GetKvNamespace`、`esa:CreateKvNamespace`、`esa:PutKv`、`esa:DeleteKv`、`esa:GetRoutine`、`esa:CreateRoutine`、`esa:GetRoutineStagingCodeUploadInfo`、`esa:CommitRoutineStagingCode`、`esa:CreateRoutineCodeDeployment`、`esa:ListRoutineCodeVersions`、`esa:DeleteRoutineCodeVersion`、`esa:ListRoutineRoutes`、`esa:CreateRoutineRoute`、`esa:UpdateRoutineRoute` |
| CDN（`aliyun.cdn`） | `cdn:SetCdnDomainSSLCertificate`、`cdn:DescribeDomainCertificateInfo` |
| 全站加速 DCDN（`aliyun.dcdn`） | `dcdn:SetDcdnDomainSSLCertificate`、`dcdn:DescribeDcdnDomainCertificateInfo` |
| 数字证书管理服务（`aliyun.cas`） | `yundun-cert:UploadUserCertificate`、`yundun-cert:DeleteUserCertificate`、`yundun-cert:ListUserCertificateOrder` |
| 取回阿里云签发的证书（`aliyun.fetch`） | `yundun-cert:ListUserCertificateOrder`、`yundun-cert:GetUserCertificateDetail` |
| 阿里云 DNS（域名验证） | `alidns:DescribeDomains`、`alidns:DescribeDomainRecords`、`alidns:AddDomainRecord`、`alidns:DeleteDomainRecord` |

OSS、传统型负载均衡（CLB）与应用型负载均衡（ALB）目标先把证书上传到数字证书管理服务，因此除了自身的操作，还需要上表中数字证书管理服务的操作：OSS 为 `oss:PutCname`、`oss:ListCname`；CLB 与 ALB 为读取和设置监听证书的相关操作。

## 腾讯云（CAM）

CAM 策略中的操作写作 `<服务>:<接口>`，如 `ssl:UploadCertificate`。

| 用途 | 操作 |
|---|---|
| SSL 证书服务，一键更新资源（`tencentcloud.ssl`） | `ssl:UploadCertificate`、`ssl:UpdateCertificateInstance`、`ssl:DescribeCertificates`、`ssl:DeleteCertificate`；腾讯云还会检查子用户对所更新资源（如 CDN、CLB）的权限 |
| 直接部署到 CDN（`tencentcloud.cdn`） | `ssl:UploadCertificate`、`ssl:DeployCertificateInstance`、`ssl:DeleteCertificate`、`ssl:DescribeCertificates` |
| 直接部署到 EdgeOne（`tencentcloud.teo`） | `ssl:UploadCertificate`、`ssl:DeleteCertificate`、`ssl:DescribeCertificates`、`teo:ModifyHostsCertificate` |
| 取回腾讯云签发的证书（`tencentcloud.fetch`） | `ssl:DescribeCertificates`、`ssl:DownloadCertificate` |
| 腾讯云免费证书（`tencentcloud.free`） | `ssl:ApplyCertificate`、`ssl:DescribeCertificate`、`ssl:CompleteCertificate`、`ssl:DownloadCertificate`，以及下面 DNSPod 的操作（腾讯云自行添加验证记录） |
| DNSPod（域名验证） | `dnspod:DescribeDomainList`、`dnspod:DescribeRecordList`、`dnspod:CreateRecord`、`dnspod:DeleteRecord` |

## AWS（IAM）

| 用途 | 操作 |
|---|---|
| Certificate Manager（`aws.acm`） | `acm:ImportCertificate`、`acm:AddTagsToCertificate`、`acm:DescribeCertificate`、`acm:ListCertificates` |
| CloudFront（`aws.cloudfront`） | 上述 ACM 操作（`us-east-1` 区域），以及 `cloudfront:GetDistributionConfig`、`cloudfront:UpdateDistribution` |
| Route 53（域名验证） | `route53:ListHostedZonesByName`、`route53:ListResourceRecordSets`、`route53:ChangeResourceRecordSets`、`route53:GetChange` |

## Cloudflare（API 令牌）

| 用途 | 权限 |
|---|---|
| 域名验证 | 区域 → 区域 → 读取；区域 → DNS → 编辑 |
| 自定义证书（`cloudflare.custom`） | 区域 → SSL 和证书 → 编辑 |
| 边缘应答（`cloudflare.edge`） | 见[域名验证](../usage/validation.zh-CN.md) |

在“区域资源”中把令牌限定在证书所在的区域。

## 其他

华为云 CDN 目标调用 `ShowDomainFullConfig`、`UpdateDomainFullConfig`，ELB 目标调用 `ListCertificates`、`ShowCertificate`、`CreateCertificate`、`UpdateCertificate`、`ShowListener`、`UpdateListener`，请授予允许这些操作的 IAM 策略。宝塔、SSH 等面板和服务器使用各自的密钥或账号，能单独创建时请专门为 JEC 创建一个。
