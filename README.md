YouTube Stream Tracker
======================

軽量な YouTube ライブ監視サービスのリポジトリです。主な目的はライブ配信の映像/音声の健全性を監視し、問題発生時にWebhookで通知することです。

- コンポーネント: API Gateway（REST API）, Worker（ストリーム監視プロセス）, DB 層, Webhook 配信
- 言語: Go
- コンテナ: Docker / docker-compose / Helm マニフェストを含む
- 主なユースケース: モニタの登録 → Kubernetes 上に Worker Pod が立ち上がりストリームを監視 → 異常を検知したら Webhook 送信

主なファイル
- `cmd/gateway/main.go` - API Gateway のエントリポイント（HTTP サーバ、DB接続、Kubernetes リコンシリエータ）
- `cmd/worker/main.go` - Worker のエントリポイント（個別監視ジョブ）
- `internal/worker/worker.go` - Worker の主要ロジック（待機→監視→解析→Webhook 送信）
- `internal/api/handlers.go` - Gateway の HTTP ハンドラ（モニタ作成、取得、停止、内部ステータス更新）
- `internal/config/config.go` - 環境変数からの設定読み込み（必須項目を含む）
- `internal/webhook/webhook.go` - Webhook の署名/送信と再試行ロジック
- `internal/db/models.go` - DB モデルとデフォルト設定
- `docker-compose.yaml`, `Dockerfile.*`, `helm/` - デプロイやローカル起動に関する定義

クイックスタート（ローカル、Docker Compose）
1. 環境変数を用意します（最低限）:
   - Gateway: `DB_DSN`（または`DATABASE_URL`）、`API_KEY`、`INTERNAL_API_KEY`、`WEBHOOK_SIGNING_KEY`
   - Worker（個別起動時）: `MONITOR_ID`、`STREAM_URL`、`CALLBACK_URL`、`INTERNAL_API_KEY`、`WEBHOOK_URL`、`WEBHOOK_SIGNING_KEY`
2. docker-compose を利用する場合:
   - `docker-compose up --build` で Gateway / Worker / DB 等を立ち上げます（compose ファイルを確認してください）。
3. Gateway が起動したらヘルスチェック:
   - `GET /healthz` と `GET /readyz` を確認します。

API（外部）
- Base: `/api/v1` （`API_KEY` による認可が必要）
- POST `/api/v1/monitors` - モニタ作成
  - Body 例:
    ```json
    {
      "stream_url": "https://www.youtube.com/watch?v=VIDEO_ID",
      "callback_url": "https://example.com/internal-callback",
      "config": { /* 任意 */ },
      "metadata": {"key": "value"}
    }
    ```
- GET `/api/v1/monitors` - モニタ一覧
- GET `/api/v1/monitors/:monitor_id` - 単一取得
- DELETE `/api/v1/monitors/:monitor_id` - 停止

内部 API（Worker → Gateway）
- Base: `/internal/v1` （`INTERNAL_API_KEY` 必須）
- PUT `/internal/v1/monitors/:monitor_id/status` - Worker がステータス/統計を更新するために使用します。

Webhook 仕様
- 署名: `X-Signature-256: sha256=<hex>` と `X-Timestamp` ヘッダを付与します。検証用ロジックは `internal/webhook/VerifySignature` を参照してください（タイムウィンドウは 5 分）。
- 送信は再試行を含む堅牢な実装で、最終的に失敗した場合は Worker を終了方向に遷移させる動作があります（`internal/webhook` と `internal/worker` を参照）。

主要な設定（抜粋）
- Gateway 側必須環境変数: `DB_DSN` / `DATABASE_URL`, `API_KEY`, `INTERNAL_API_KEY`, `WEBHOOK_SIGNING_KEY` (`internal/config/config.go` を参照)
- Worker 側必須環境変数: `MONITOR_ID`, `STREAM_URL`, `CALLBACK_URL`（`internal/config/config.go` を参照）
- FFmpeg/yt-dlp 等の外部実行バイナリは環境変数でパスを指定できます（デフォルトは `ffmpeg`, `ffprobe`, `yt-dlp`, `streamlink`）

開発者向け
- ビルド: `go build ./...` または個々のバイナリを `go build ./cmd/gateway` 等で作成
- テスト: `go test ./...`（パッケージ単位でも可）
- 依存管理: `go.mod` / `go.sum` を使用
- ログ: JSON ログ（`internal/log`）

運用/デプロイ
- Kubernetes 環境での実行を想定しており、`internal/k8s` にリコンシリエータと Pod 作成ロジックがあります。`helm/` ディレクトリには Helm チャートの雛形が含まれています。
- コンテナイメージは `Dockerfile.gateway`, `Dockerfile.worker` などで定義済みです。
