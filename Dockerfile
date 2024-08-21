# build env
FROM golang:1.22 AS build-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG release=
RUN make build RELEASE=$release GOOS=$TARGETOS GOARCH=$TARGETARCH

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && apt-get clean && rm -rf /var/lib/apt/lists/*
RUN update-ca-certificates
COPY --from=build-env /src/bin /app
EXPOSE 8080
ENTRYPOINT ["./dora-explorer"]
CMD []
