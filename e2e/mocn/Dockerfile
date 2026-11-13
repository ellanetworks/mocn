FROM node:24 AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
RUN go build -o /mocn ./cmd/mocn

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates iproute2 tcpdump procps \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /mocn /usr/local/bin/mocn
ENTRYPOINT ["mocn"]
CMD ["--config", "/mocn.yaml"]
