# Observability Stack Deployment

qumo-relay のオブザーバビリティスタック構成。

## 構成

```
┌─────────────┐     ┌─────────────────┐     ┌────────────┐
│ qumo-relay  │────▶│  OTel Collector │────▶│   Jaeger   │
│  :4433      │     │    :4317        │     │  :16686    │
└─────────────┘     └─────────────────┘     └────────────┘
       │
       │ /metrics
       ▼
┌─────────────┐     ┌─────────────────┐
│ Prometheus  │────▶│    Grafana      │
│   :9090     │     │    :3000        │
└─────────────┘     └─────────────────┘
```

## クイックスタート

```bash
# 起動
cd deploy
docker compose up -d

# 確認
docker compose ps

# ログ
docker compose logs -f otel-collector
```

## アクセス

| サービス | URL | 説明 |
|---------|-----|------|
| Jaeger | http://localhost:16686 | 分散トレーシング |
| Prometheus | http://localhost:9090 | メトリクス |
| Grafana | http://localhost:3000 | ダッシュボード (admin/admin) |

## qumo-relay の設定

```bash
# 環境変数で OTel Collector を指定
export OTEL_ENDPOINT=localhost:4317
export OTEL_SAMPLING_RATE=1.0

# 起動
./qumo-relay -config config.relay.yaml
```

または config.yaml で設定：

```yaml
server:
  health_check_addr: ":9090"  # /metrics エンドポイント
```

## ファイル構成

```
deploy/
├── docker compose.yaml       # Docker Compose 定義
├── otel-collector-config.yaml # OTel Collector 設定
├── prometheus.yaml           # Prometheus 設定
└── grafana/
    └── provisioning/
        └── datasources/
            └── datasources.yaml  # Grafana データソース
```

## カスタマイズ

### サンプリング率の調整

本番環境では低いサンプリング率を推奨：

```bash
export OTEL_SAMPLING_RATE=0.01  # 1%
```

### Prometheus ターゲットの変更

`prometheus.yaml` を編集：

```yaml
scrape_configs:
  - job_name: 'qumo-relay'
    static_configs:
      - targets: ['your-host:9090']
```

### ログを Loki に送信

`otel-collector-config.yaml` で Loki エクスポーターを有効化：

```yaml
exporters:
  loki:
    endpoint: http://loki:3100/loki/api/v1/push

service:
  pipelines:
    logs:
      exporters: [loki]
```

## トラブルシューティング

### OTel Collector に接続できない

```bash
# Collector のヘルスチェック
curl http://localhost:13133/

# ログ確認
docker compose logs otel-collector
```

### メトリクスが表示されない

```bash
# qumo-relay のメトリクスエンドポイント確認
curl http://localhost:9090/metrics

# Prometheus のターゲット確認
# http://localhost:9090/targets
```
