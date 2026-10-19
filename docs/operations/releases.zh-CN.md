# 校验发布文件

[English](releases.md)

每个版本的校验和文件与容器镜像均由发布流水线使用 [cosign](https://github.com/sigstore/cosign) 进行无密钥签名。

## 校验压缩包

从[发布页](https://github.com/XMZO/JustEnoughCerts/releases)下载压缩包、`checksums.txt` 与 `checksums.txt.sigstore.json`，然后执行：

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

## 校验镜像

```sh
cosign verify ghcr.io/xmzo/justenoughcerts:<版本> \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## 软件物料清单

每个压缩包附带 SPDX 格式的 SBOM（`.sbom.json`），每个镜像附带 SBOM 与构建来源证明。
