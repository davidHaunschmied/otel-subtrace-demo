#!/usr/bin/env bash
# Astroshop subtrace demo -> Dynatrace (gan7755d), core services only,
# all containers inside the resource-capped astroshop.slice.
#   ./astroshop.sh up | down | ps | logs [service]
set -euo pipefail
cd "$(dirname "$0")"
compose() {
  docker compose -p astroshop \
    --env-file .env --env-file .env.override --env-file .env.dynatrace \
    -f compose.yaml -f compose.extras.yaml -f compose.remote.yaml "$@"
}
case "${1:-}" in
  up)   systemctl is-active --quiet astroshop.slice || sudo systemctl start astroshop.slice
        compose up --detach --remove-orphans --pull always ;;  # DEMO_VERSION=latest is a moving tag
  down) compose down --remove-orphans ;;
  ps)   compose ps ;;
  logs) shift; compose logs --tail 100 "$@" ;;
  *)    echo "usage: $0 up|down|ps|logs [service]" >&2; exit 2 ;;
esac
