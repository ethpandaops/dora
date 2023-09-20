# build env
FROM golang:1.20 AS build-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG release=
RUN <<EOR
  GITVER=$(git rev-parse --short HEAD)
  BUILDTIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  RELEASE=$release
  CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /bin/dora-explorer -ldflags="-s -w -X 'github.com/pk910/light-beaconchain-explorer/utils.BuildVersion=\"${VERSION}\"' -X 'github.com/pk910/light-beaconchain-explorer/utils.BuildRelease=\"${RELEASE}\"' -X 'github.com/pk910/light-beaconchain-explorer/utils.BuildVersion=\"${VERSION}\"'" ./cmd/explorer
EOR

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
RUN update-ca-certificates
COPY --from=build-env /bin/dora-explorer /app
EXPOSE 8080
ENTRYPOINT ["./dora-explorer"]
CMD ["-config=./config/default.config.yml"]
