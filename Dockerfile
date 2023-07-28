# build env
FROM golang:1.20 AS build-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
ADD . /src
ARG target=linux
RUN make -B $target

# final stage
FROM alpine:latest
WORKDIR /app
COPY --from=build-env /src/bin /app/
COPY --from=build-env /src/config /app/config
EXPOSE 8080
ENTRYPOINT ["./explorer_linux_amd64"]
CMD ["-config=./config/default.config.yml"]