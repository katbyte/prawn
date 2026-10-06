# syntax=docker/dockerfile:1

# no default: .go-version is the single source of truth and make/CI pass it in, so the builder can
# never lag go.mod's go directive (the official go images pin GOTOOLCHAIN=local, so an older
# builder fails the build rather than fetching a newer toolchain). build by hand with:
#   docker build --build-arg GO_VERSION=$(cat .go-version) .
ARG GO_VERSION

# the build stage runs on the builder's own architecture and cross-compiles for the target, so the
# multi-arch image builds in seconds rather than minutes under qemu emulation
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=docker

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -mod=vendor \
    -ldflags "-s -w -X github.com/katbyte/go-kt/version.Version=${VERSION} -X github.com/katbyte/go-kt/version.GitCommit=${GIT_COMMIT}" \
    -o /out/prawn .

# dcron refreshes the page on a schedule, ca-certificates let prawn reach github, git is for the
# release markers and the checks tab when a provider checkout is mounted, tzdata makes TZ work.
# upgrade first so a release rebuild picks up alpine security fixes the base image tag has not
# been rebuilt with yet
FROM alpine:3.24
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates dcron git tzdata
# the provider checkout lives on the volume, often owned by the host's user: git refuses a repository owned by
# someone else unless told it is safe, and every git call in it would fail
RUN git config --system --add safe.directory '*'

# the volume: prawn's defaults put prs.db and report/explore.html under the working directory
WORKDIR /data
COPY --from=build /out/prawn /usr/bin/prawn
COPY scripts/entry.sh scripts/run.sh /app/scripts/
RUN chmod +x /app/scripts/entry.sh /app/scripts/run.sh

EXPOSE 8765
CMD ["/app/scripts/entry.sh"]
