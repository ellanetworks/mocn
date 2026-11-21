# Upgrade

This guide upgrades Ella MOCN to a new release.

## Prerequisites

- **Ella MOCN**: installed, as in [Install](<https://docs.ellanetworks.com/mocn/how_to/install/index.md>).
- **Target release**: its version, such as `v0.0.2`.

## 1\. Upgrade

Refresh the snap:

```
sudo snap refresh ella-mocn
```

In the Ella MOCN repository, check out the target release:

```
git fetch --tags
git checkout <version>
```

Build the frontend:

```
npm install --prefix ui
npm run build --prefix ui
```

Build Ella MOCN:

```
go build -o mocn -ldflags "-s -w -X github.com/ellanetworks/mocn/version.GitCommit=$(git rev-parse HEAD)" ./cmd/mocn
```

Stop Ella MOCN, then start it:

```
./mocn --config mocn.yaml
```

In the directory of `docker-compose.yaml`, pull the new image and restart the container:

```
docker compose pull
docker compose up -d
```

1. Upgrade the chart:

   ```
   helm upgrade ella-mocn oci://ghcr.io/ellanetworks/charts/ella-mocn --version <version> --namespace ella -f values.yaml
   ```
2. Wait for the rollout:

   ```
   kubectl -n ella rollout status statefulset/ella-mocn
   ```

## 2\. Verify

Check the running version in the UI's top bar.
