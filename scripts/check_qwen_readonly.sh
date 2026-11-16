#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '=== llama-server (read only diagnostics) ==='
systemctl status llama-server --no-pager || true
curl -fsS --max-time 10 http://127.0.0.1:8080/v1/models || true
printf '\n%s\n' '=== nvidia-smi ==='
nvidia-smi || true
printf '%s\n' '=== No models, services or settings changed ==='
