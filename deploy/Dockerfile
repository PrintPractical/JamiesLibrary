# Jamie's Library image. Build from the repository root:
#   docker build -f deploy/Dockerfile --build-arg VERSION=1.0.0 -t jamieslibrary .
# Both architectures (needs a buildx builder that can push or load multi-platform):
#   docker buildx build -f deploy/Dockerfile --platform linux/amd64,linux/arm64 -t jamieslibrary .
# The two build stages always run on the builder's own architecture and cross-compile,
# so an arm64 image needs no emulation to build.

# Step 1: the web app, built once whatever the target.
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS web
WORKDIR /src
COPY web/package.json web/package-lock.json web/
RUN cd web && npm ci
COPY web web
# The build writes into ../internal/webui/dist, where the Go program embeds it from.
RUN mkdir -p internal/webui/dist && cd web && npm run build

# Step 2: the program: static (no CGO), paths trimmed, version stamped in.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/jamieslibrary ./cmd/jamieslibrary
# The data directory, made here because the final image has no shell. UID 65532 is the
# base image's "nonroot" user.
RUN mkdir /out/data && chmod 700 /out/data

# Step 3: distroless static base (no shell, no package manager), pinned by digest.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/jamieslibrary /usr/local/bin/jamieslibrary
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
# A new volume mounted on /data takes this folder's owner. A bind mount must be owned by 65532.
VOLUME /data
ENV JL_DATA_DIR=/data \
    # Keeps the Go runtime's memory under 400 MiB so a 20,000-copy library stays below 512 MB.
    GOMEMLIMIT=400MiB
# 8443 is the app over HTTPS, 8080 the trust guide and the redirect to HTTPS.
EXPOSE 8443 8080
ENTRYPOINT ["/usr/local/bin/jamieslibrary"]
CMD ["serve"]
