#!/bin/sh
set -e

# Auto-generate config files from environment variables if not provided
generate_relay_config() {
    cat > /tmp/config.relay.yaml <<EOF
server:
  address: "${RELAY_ADDR:-0.0.0.0:4433}"
  cert_file: "${CERT_FILE:-certs/server.crt}"
  key_file: "${KEY_FILE:-certs/server.key}"

relay:
  group_cache_size: ${GROUP_CACHE_SIZE:-100}
  frame_capacity: ${FRAME_CAPACITY:-1500}
EOF

    # Add SDN config if SDN_URL is set
    if [ -n "$SDN_URL" ]; then
        cat >> /tmp/config.relay.yaml <<EOF

sdn:
  url: "$SDN_URL"
  relay_name: "${RELAY_NAME:-relay-${HOSTNAME}}"
  heartbeat_interval_sec: ${HEARTBEAT_INTERVAL:-30}
EOF

        # Add topology registration fields (neighbors, region, address)
        # Relays self-register via PUT /relay/<name> heartbeat
        if [ -n "$NEIGHBORS" ]; then
            echo "  address: \"${RELAY_MOQT_ADDR:-https://${RELAY_NAME:-relay}:4433}\"" >> /tmp/config.relay.yaml
            if [ -n "$REGION" ]; then
                echo "  region: \"$REGION\"" >> /tmp/config.relay.yaml
            fi
            echo "  neighbors:" >> /tmp/config.relay.yaml
            # Parse comma-separated neighbors: "relay-london:250,relay-newyork:80"
            echo "$NEIGHBORS" | tr ',' '\n' | while IFS=: read -r name cost; do
                echo "    $name: $cost" >> /tmp/config.relay.yaml
            done
        fi
    fi
}

generate_sdn_config() {
    cat > /tmp/config.sdn.yaml <<EOF
graph:
  listen_addr: "${SDN_ADDR:-:8090}"
  data_dir: "${DATA_DIR:-./data}"
  sync_interval_sec: ${SYNC_INTERVAL:-10}
  node_ttl_sec: ${NODE_TTL_SEC:-90}
EOF

    # Add peer URL if specified
    if [ -n "$PEER_URL" ]; then
        echo "  peer_url: \"$PEER_URL\"" >> /tmp/config.sdn.yaml
    fi
}

# Generate self-signed certificates if in insecure mode and certs don't exist
generate_insecure_certs() {
    if [ "$INSECURE" = "true" ] && [ ! -f certs/server.crt ]; then
        echo "🔓 INSECURE mode: Generating self-signed certificates..."
        mkdir -p certs
        
        # Generate self-signed certificate (valid for 365 days)
        openssl req -x509 -newkey rsa:2048 -nodes \
            -keyout certs/server.key \
            -out certs/server.crt \
            -days 365 \
            -subj "/CN=localhost" \
            -addext "subjectAltName=DNS:localhost,DNS:*.localhost,IP:127.0.0.1" \
            2>/dev/null || {
                echo "⚠️  OpenSSL not available, using placeholder certs"
                echo "placeholder" > certs/server.key
                echo "placeholder" > certs/server.crt
            }
        
        echo "✅ Self-signed certificates generated"
    fi
}

# Main entrypoint logic
COMMAND=$1
CONFIG_FILE=$2

case "$COMMAND" in
    relay)
        # If no config file provided, generate from env vars
        if [ "$CONFIG_FILE" = "-config" ] && [ ! -f "$3" ]; then
            echo "📝 Generating relay config from environment variables..."
            generate_relay_config
            generate_insecure_certs
            exec /app/qumo relay -config /tmp/config.relay.yaml
        else
            generate_insecure_certs
            exec /app/qumo "$@"
        fi
        ;;
    sdn)
        # If no config file provided, generate from env vars
        if [ "$CONFIG_FILE" = "-config" ] && [ ! -f "$3" ]; then
            echo "📝 Generating SDN config from environment variables..."
            generate_sdn_config
            generate_insecure_certs
            mkdir -p "${DATA_DIR:-./data}"
            exec /app/qumo sdn -config /tmp/config.sdn.yaml
        else
            generate_insecure_certs
            mkdir -p "${DATA_DIR:-./data}"
            exec /app/qumo "$@"
        fi
        ;;
    *)
        exec /app/qumo "$@"
        ;;
esac