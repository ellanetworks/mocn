# Install

Choose one of the installation methods below.

Install the Ella MOCN snap:

```
sudo snap install ella-mocn
```

Configure Ella MOCN:

```
sudo vim /var/snap/ella-mocn/common/mocn.yaml
```

Start Ella MOCN:

```
sudo snap start --enable ella-mocn.mocnd
```

Install the required dependencies:

```
sudo snap install go --channel=1.27/stable --classic
sudo snap install node --channel=24/stable --classic
sudo apt update
sudo apt -y install gcc
```

Clone the Ella MOCN repository:

```
git clone https://github.com/ellanetworks/mocn.git
cd mocn
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

Configure Ella MOCN:

```
vim mocn.yaml
```

Start Ella MOCN:

```
./mocn --config mocn.yaml
```

Create a new directory:

```
mkdir ella-mocn
cd ella-mocn
```

Copy the following file into this directory:

docker-compose.yaml

```
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
      ran:
        signalling:
          address: 0.0.0.0
          s1_port: 36412
          ng_port: 38412

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

```
docker compose up -d
```

Ensure your Kubernetes cluster meets the [Kubernetes requirements](<https://docs.ellanetworks.com/mocn/reference/system_reqs/index.md#kubernetes>).

Create `values.yaml`. To give Ella MOCN an S1 and NG address on the radios' network, set `address` to that address and `master` to the host interface that connects to your radios:

```
ran:
  address: 192.168.252.7/24
  networkAttachment:
    master: eth1
```

To expose S1 and NG through a Service on the pod network instead, set the Service type:

```
ran:
  service:
    type: LoadBalancer
```

Deploy Ella MOCN:

```
helm install ella-mocn oci://ghcr.io/ellanetworks/charts/ella-mocn --version 0.0.1 --namespace ella --create-namespace -f values.yaml
```

Open the UI and API on your machine:

```
kubectl -n ella port-forward service/ella-mocn 5030:5030
```

The UI and API are at `http://localhost:5030`. See the [chart reference](<https://github.com/ellanetworks/mocn/tree/main/charts/ella-mocn>) for all values.

Ella MOCN's UI and API are at `http://<host-ip>:5030`. Open the UI and create the first user.

See the [configuration reference](<https://docs.ellanetworks.com/mocn/reference/config_file/index.md>) for all options.
