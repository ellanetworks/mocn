---
description: Step-by-step instructions to install Ella MOCN.
---

# Install

Choose one of the installation methods below.

=== "Source"

    Install the required dependencies:

    ```shell
    sudo snap install go --channel=1.27/stable --classic
    sudo snap install node --channel=24/stable --classic
    sudo apt update
    sudo apt -y install gcc
    ```

    Clone the Ella MOCN repository:

    ```shell
    git clone https://github.com/ellanetworks/mocn.git
    cd mocn
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

    Configure Ella MOCN:

    ```shell
    vim mocn.yaml
    ```

    Start Ella MOCN:

    ```shell
    ./mocn --config mocn.yaml
    ```

=== "Docker"

    Create a new directory:

    ```shell
    mkdir ella-mocn
    cd ella-mocn
    ```

    Copy the following file into this directory:

    ```yaml title="docker-compose.yaml"
    configs:
      ella_mocn_config:
        content: |
          logging:
            level: info
          db:
            path: /data/mocn.db
          api:
            address: 0.0.0.0
            port: 5030
          s1:
            address: 0.0.0.0
            port: 36412

    services:
      ella-mocn:
        image: ghcr.io/ellanetworks/ella-mocn:latest
        configs:
          - source: ella_mocn_config
            target: /mocn.yaml
        entrypoint: /bin/mocn --config /mocn.yaml
        restart: unless-stopped
        network_mode: host
        volumes:
          - ella-mocn-data:/data

    volumes:
      ella-mocn-data:
    ```

    Start the Ella MOCN container:

    ```shell
    docker compose up -d
    ```

=== "Kubernetes"

    Ensure your Kubernetes cluster nodes meet the [system requirements](../reference/system_reqs.md).

    Download the Ella MOCN manifests:

    ```shell
    git clone --depth 1 https://github.com/ellanetworks/mocn.git
    ```

    Deploy Ella MOCN:

    ```shell
    kubectl create namespace ella
    kubectl apply -k mocn/k8s
    ```

    Open the UI and API on your machine:

    ```shell
    kubectl port-forward -n ella svc/ella-mocn 5030:5030
    ```

    S1 is at `ella-mocn-s1.ella.svc.cluster.local:36412`.

Ella MOCN's UI and API are at `http://<host-ip>:5030`. Open the UI and create the first user.

See the [configuration reference](../reference/config_file.md) for all options.
