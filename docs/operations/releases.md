# Verifying releases

[中文](releases.zh-CN.md)

The release workflow signs the checksum file and the container images of
every version with [cosign](https://github.com/sigstore/cosign), keyless.

## Archives

Download the archive, `checksums.txt` and `checksums.txt.sigstore.json`
from the [releases page](https://github.com/XMZO/JustEnoughCerts/releases),
then run:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

## Images

```sh
cosign verify ghcr.io/xmzo/justenoughcerts:<version> \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Software bills of materials

Each archive comes with an SPDX SBOM (`.sbom.json`), and each image with an
SBOM and build provenance attestations.
