# node build env
FROM node:20 AS node-env
WORKDIR /app
COPY ui-package/package.json /app
RUN npm install
COPY ui-package /app
RUN npm run build

# go build env
FROM golang:1.22 AS go-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
COPY . .
COPY --from=node-env /app/dist /src/ui-package/dist
ARG TARGETOS
ARG TARGETARCH
ARG release=
RUN make build RELEASE=$release GOOS=$TARGETOS GOARCH=$TARGETARCH

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && apt-get clean && rm -rf /var/lib/apt/lists/*
RUN update-ca-certificates
COPY --from=go-env /src/bin /app
EXPOSE 8080
ENTRYPOINT ["./dora-explorer"]
CMD []
