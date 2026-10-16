# syntax=docker/dockerfile:1

# Two targets build the same runtime image:
#
#   source (default)  builds jec from this repository: docker build .
#   release           copies prebuilt binaries laid out as <os>/<arch>/jec in
#                     the build context, as the release workflow does. Nothing
#                     runs for the target platform, so multi-arch builds need
#                     no emulation.

# Empty directories for the runtime image, created on the build platform:
# scratch has no shell to make them.
FROM --platform=$BUILDPLATFORM busybox:1.38@sha256:fd7dc98638c8e305f4dc34e979f1c0fdfdcaeb0fbf8fcff77ae834b6da3d7e6e AS rootfs
RUN mkdir -p /rootfs/data /rootfs/tmp \
 && chown 65532:65532 /rootfs/data && chmod 0700 /rootfs/data \
 && chmod 1777 /rootfs/tmp

FROM scratch AS runtime
# Copying the parent keeps the owner and mode of each directory.
COPY --from=rootfs /rootfs/ /
ENV JEC_DATA_DIR=/data
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 CMD ["/jec", "healthcheck"]
ENTRYPOINT ["/jec"]
CMD ["serve"]

FROM runtime AS release
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/jec /jec

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo.version=${VERSION}" \
      -o /out/jec ./cmd/jec

FROM runtime AS source
COPY --from=build /out/jec /jec
