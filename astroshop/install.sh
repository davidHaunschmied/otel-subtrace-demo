#!/usr/bin/env bash
# Set up the OpenTelemetry Demo (astroshop) with the subtrace showcase and a
# Dynatrace backend. Run on the target host:
#
#   astroshop/install.sh [target-dir]      # default: ~/opentelemetry-demo
#
# Then put DT_OTLP_ENDPOINT and DT_INGEST_TOKEN into <target-dir>/.env.dynatrace
# and run <target-dir>/astroshop.sh up
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
target="${1:-$HOME/opentelemetry-demo}"
# Commit the overlays were written and tested against.
demo_commit=6c2a814b9c74b72d5d6a2fd22281dad4ba246512

if [[ ! -d "$target/.git" ]]; then
  git init -q "$target"
  git -C "$target" remote add origin https://github.com/open-telemetry/opentelemetry-demo.git
fi
git -C "$target" fetch -q --depth 1 origin "$demo_commit"
git -C "$target" checkout -q --force "$demo_commit"

install -m 0644 "$here/compose.extras.yaml" "$here/compose.remote.yaml" "$target/"
install -m 0644 "$here/otelcol-config-extras.yml" "$target/src/otel-collector/otelcol-config-extras.yml"
install -m 0755 "$here/astroshop.sh" "$target/astroshop.sh"

# Fail 25% of payments so the error-propagation feature has data.
python3 - "$target/src/flagd/demo.flagd.json" <<'EOF'
import json, sys
path = sys.argv[1]
with open(path) as f:
    flags = json.load(f)
flags["flags"]["paymentFailure"]["defaultVariant"] = "25%"
with open(path, "w") as f:
    json.dump(flags, f, indent=2, ensure_ascii=False)
    f.write("\n")
EOF

# Resource budget for hosts shared with other workloads (see astroshop.slice).
sudo install -m 0644 "$here/astroshop.slice" /etc/systemd/system/astroshop.slice
sudo systemctl daemon-reload
sudo systemctl start astroshop.slice

if [[ ! -f "$target/.env.dynatrace" ]]; then
  (umask 077; cat > "$target/.env.dynatrace" <<'EOF'
# Dynatrace backend for the astroshop subtrace demo. Do not commit.
# Platform token (dt0s16...) with OTLP ingest permissions for traces, metrics and logs.
DT_OTLP_ENDPOINT=https://<environment-id>.live.dynatrace.com/api/v2/otlp
DT_INGEST_TOKEN=
EOF
  )
  echo "Fill in $target/.env.dynatrace, then run $target/astroshop.sh up"
fi
grep -qx '.env.dynatrace' "$target/.git/info/exclude" 2>/dev/null || echo '.env.dynatrace' >> "$target/.git/info/exclude"
echo "Installed into $target (opentelemetry-demo ${demo_commit:0:7})"
