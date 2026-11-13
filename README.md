# Ella MOCN (beta)

<p align="center">
  <img src="docs/images/logo.svg" alt="Ella MOCN" width="120"/>
</p>

**Ella MOCN** integrates your neutral host radio network with mobile operators. It is a Multi-Operator Core Network (MOCN) gateway: your radios connect to Ella MOCN, and Ella MOCN connects them to each operator's core.

## Key Features

- **One gateway for every operator**: radios connect once, to Ella MOCN, which appears to them as a single MME (4G) or AMF (5G), and to each operator as one eNB and one gNB with the IDs that operator assigned.
- **Several operators**: each with its PLMNs and its own pools of MMEs and AMFs, from any vendor.
- **MME and AMF selection**: each UE goes to the operator of the PLMN it selected, and to the MME or AMF that already knows it, or one weighted by capacity.
- **Untouched signalling**: NAS and security keys pass through byte for byte, never decoded, logged or stored.
- **Visibility**: the state of every radio, MME and AMF, and UE connections by radio and operator.
- **Easy to operate**: a single binary with an embedded database, a web UI, an HTTP API, and Prometheus metrics. Install with Docker or from source.

## Quick Links

- [Documentation](docs/index.md)
- [Website](https://ellanetworks.com/)
- [Ella Core](https://github.com/ellanetworks/core)
- [Contributing](CONTRIBUTING.md)

## License

Ella MOCN is licensed under Business Source (BUSL-1.1).

You need a [Production License](https://ellanetworks.com/pricing) to run Ella MOCN in production. For more information, view our [License FAQ](https://ellanetworks.com/license-faq).
