---
description: A MOCN gateway that integrates your neutral host radio network with mobile operators.
---

# Ella MOCN

<p align="center">
  <img src="images/logo.svg" alt="Ella MOCN Logo" width="200" />
</p>

**Ella MOCN** integrates your neutral host radio network with mobile operators. It is a Multi-Operator Core Network (MOCN) gateway: your radios connect to Ella MOCN, and Ella MOCN connects them to each operator's core.

## Key features

- **Multi-Operator**: Share one 4G and 5G radio network between several operators. Radios connect to Ella MOCN, and each operator sees a single base station.
- **4G and 5G Compliant**: Ella MOCN integrates with 4G and 5G networks following 3GPP standards. It has been validated with commercial radios, phones and cores.
- **User-Plane Aggregation**: Optionally carry subscriber traffic through Ella MOCN's eBPF-based data plane, so Ella MOCN acts as a single point of contact for operators.
- **Lightweight**: A single binary with an embedded UI, API and database. Install from source, with Docker or on Kubernetes.
- **Easy to Operate**: Manage operators and radios through a web interface or a RESTful API with an OpenAPI specification. Monitor every radio, MME and AMF, and each operator's traffic, with logs, metrics (Prometheus), traces (OTel) and profiles.

## In this documentation

<div class="grid cards" markdown>

-   [__Tutorials__](tutorials/index.md)

    ---

    **Start here**: a hands-on introduction to Ella MOCN.

-   [__How-to Guides__](how_to/index.md)

    ---

    **Step-by-step guides** covering key operations and common tasks.

-   [__Reference__](reference/index.md)

    ---

    **Technical information** - API, configuration, metrics, and more.

-   [__Explanation__](explanation/index.md)

    ---

    **Discussion and clarification** of key topics.

</div>
