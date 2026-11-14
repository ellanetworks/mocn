#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"
sha256sum <Dockerfile | cut -c1-16
