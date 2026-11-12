# Contributing

## Build

### From source

```sh
npm install --prefix ui
npm run build --prefix ui
go build -o mocn -ldflags "-s -w -X github.com/ellanetworks/mocn/version.GitCommit=$(git rev-parse HEAD)" ./cmd/mocn
```

### Container Image

```sh
rockcraft pack
```

## Run

```sh
./mocn --config mocn.yaml
```

## Test

```sh
go test ./...
npm test --prefix ui
```

The UI end-to-end tests build the UI and Ella MOCN from this checkout, and run it on port 5030 with a fresh database. They need Go and Playwright's Chromium:

```sh
npx --prefix ui playwright install chromium
npm run test:e2e --prefix ui
```

Set `ELLA_E2E_PORT` to run Ella MOCN on another port, or `ELLA_E2E_BASE_URL` to test an Ella MOCN that is already running.

## Lint

```sh
golangci-lint run ./...
npm run lint --prefix ui
npm run format:check --prefix ui
npm run typecheck --prefix ui
```

## UI development

```sh
npm run dev --prefix ui
```

The development server listens on port 3030 and proxies `/api` to Ella MOCN on `localhost:5030`. Set `MOCN_API_PROXY_TARGET` to proxy elsewhere.
