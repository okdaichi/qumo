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
  node_id: "${RELAY_NAME:-relay-${HOSTNAME}}"
  region: "${REGION:-}"
  group_cache_size: ${GROUP_CACHE_SIZE:-100}
  frame_capacity: ${FRAME_CAPACITY:-1500}
EOF

    # Add peers config if PEERS is set (comma-separated: "moqt://relay-a:4433,moqt://relay-b:4433")
    if [ -n "$PEERS" ]; then
        echo "" >> /tmp/config.relay.yaml
        echo "peers:" >> /tmp/config.relay.yaml
        echo "$PEERS" | tr ',' '\n' | while read -r addr; do
            echo "  - address: \"$addr\"" >> /tmp/config.relay.yaml
        done
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
    *)
        exec /app/qumo "$@"
        ;;
esac