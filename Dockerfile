# build env
FROM golang:1.20 AS build-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
ADD . /src
ARG target=linux
ARG release=
ENV RELEASE=$release
RUN make $target

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
RUN update-ca-certificates
COPY --from=build-env /src/bin/explorer_linux_amd64 /app
COPY --from=build-env /src/config /app/config
EXPOSE 8080
ENTRYPOINT ["./explorer_linux_amd64"]
CMD ["-config=./config/default.config.yml"]