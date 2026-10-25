# SKK Sync

Gist に置いた SKK 個人辞書へ、手元の辞書をブラウザからドラッグ＆ドロップでマージするサービス。

- 手元の辞書（複数可）と Gist の辞書をマージする。候補順は手元の辞書 → Gist の順に優先する
- SKK-JISYO.L と同じ見出し語・同じ語の候補を取り除き、送りありは降順・送りなしは昇順にソートする
- 入力は EUC-JP / UTF-8 / Shift_JIS を自動判別する。Gist には UTF-8（`coding: utf-8` ヘッダ付き）で保存する
- `/dl/euc-jp?gist=<Gist URL or ID>[&file=<ファイル名>]` で Gist の辞書を EUC-JP に変換して取得できる
  （EUC-JP で表せない候補は省略する）

マージは追加のみで、手元で削除した候補を Gist から消すことはしない。

## 起動

1. GitHub で OAuth App を作成し、Authorization callback URL を `https://<host>/callback` にする
2. SKK-JISYO.L を取得する

   ```sh
   curl -LO https://skk-dev.github.io/dict/SKK-JISYO.L.gz
   ```

3. 環境変数を設定する。[direnv](https://direnv.net/) で `.env` を読み込む前提

   ```sh
   cp .env.example .env
   # .env に GITHUB_CLIENT_ID / GITHUB_CLIENT_SECRET と SESSION_KEY（openssl rand -hex 32）を書く
   direnv allow
   ```

4. 起動する

   ```sh
   go run .
   ```

ローカルで試すときは、callback URL を `http://localhost:8080/callback` にした開発用の OAuth App を別に作り、
ブラウザでは `http://localhost:8080` を開く（`127.0.0.1` だと Cookie のホストが一致せずログインに失敗する）。

| 環境変数 | 説明 |
|---|---|
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | OAuth App の認証情報（必須） |
| `SESSION_KEY` | セッション Cookie の暗号鍵（16 進 64 文字）。未設定だと起動ごとに生成され、再起動でログアウトされる |
| `SKK_JISYO_L` | 除去に使う辞書のパス。`.gz` なら展開して読む（既定: `SKK-JISYO.L.gz`） |
| `OAUTH_REDIRECT_URL` | OAuth のコールバック URL。未設定なら OAuth App の登録値を使う |
| `GITHUB_TOKEN` | 未ログイン時のプレビュー・EUC-JP ダウンロードに使うトークン（任意。API のレート制限緩和用） |
| `ADDR` | 待ち受けアドレス（既定: `:8080`） |

## 構成

- `skk/` 辞書のパース・マージ・SKK-JISYO.L の除去・ソート・文字コード変換
- `github.go` Gist API クライアント
- `session.go` GitHub OAuth と、アクセストークンを AES-GCM で暗号化した Cookie セッション（DB なし）
- `server.go` HTTP ハンドラ
- `web/` フロントエンド（素の HTML/JS、バイナリに埋め込み）
