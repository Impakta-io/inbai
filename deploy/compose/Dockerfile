# syntax=docker/dockerfile:1

FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/inbaid ./cmd/inbaid

FROM debian:bookworm-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/inbaid /usr/local/bin/inbaid
COPY deploy/compose/inbaid-entrypoint.sh /entrypoint.sh
RUN chmod 755 /entrypoint.sh
EXPOSE 8787
ENTRYPOINT ["/entrypoint.sh"]
