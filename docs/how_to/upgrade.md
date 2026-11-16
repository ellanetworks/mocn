---
description: Step-by-step instructions to upgrade Ella MOCN to a new release.
---

# Upgrade

This guide upgrades Ella MOCN to a new release.

## Prerequisites

- **Ella MOCN**: installed, as in [Install](install.md).
- **Target release**: its version, such as `v0.0.2`.

## 1. Upgrade

=== "Source"

    In the Ella MOCN repository, check out the target release:

    ```shell
    git fetch --tags
    git checkout <version>
    ```

    Build the frontend:

    ```shell
    npm install --prefix ui
    npm run build --prefix ui
    ```

    Build Ella MOCN:

    ```shell
    go build -o mocn -ldflags "-s -w -X github.com/ellanetworks/mocn/version.GitCommit=$(git rev-parse HEAD)" ./cmd/mocn
    ```

    Stop Ella MOCN, then start it:

    ```shell
    ./mocn --config mocn.yaml
    ```

=== "Docker"

    In the directory of `docker-compose.yaml`, pull the new image and restart the container:

    ```shell
    docker compose pull
    docker compose up -d
    ```

=== "Kubernetes"

    1. Set the new image:

        ```shell
        kubectl set image statefulset/ella-mocn -n ella mocn=ghcr.io/ellanetworks/ella-mocn:<version>
        ```

    2. Wait for the rollout:

        ```shell
        kubectl rollout status statefulset/ella-mocn -n ella
        ```

## 2. Verify

Check the running version in the UI's top bar.
