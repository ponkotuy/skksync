package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"skksync/skk"
)

//go:embed web
var webFS embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	clientID := os.Getenv("GITHUB_CLIENT_ID")
	clientSecret := os.Getenv("GITHUB_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("GITHUB_CLIENT_ID と GITHUB_CLIENT_SECRET を設定してください")
	}

	key, err := sessionKey(os.Getenv("SESSION_KEY"))
	if err != nil {
		return err
	}
	sealer, err := NewSealer(key)
	if err != nil {
		return err
	}

	jisyoPath := envOr("SKK_JISYO_L", "SKK-JISYO.L.gz")
	start := time.Now()
	base, err := loadDict(jisyoPath)
	if err != nil {
		return fmt.Errorf("%s の読み込みに失敗しました: %w", jisyoPath, err)
	}
	log.Printf("loaded %s: %d entries in %v", jisyoPath, base.Len(), time.Since(start))

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	s := &Server{
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     github.Endpoint,
			// 空なら GitHub の OAuth App に登録した Callback URL が使われる
			RedirectURL: os.Getenv("OAUTH_REDIRECT_URL"),
			Scopes:      []string{"gist"},
		},
		sealer:        sealer,
		github:        &GitHub{HTTP: &http.Client{Timeout: 30 * time.Second}, APIBase: "https://api.github.com"},
		base:          base,
		fallbackToken: os.Getenv("GITHUB_TOKEN"),
		static:        static,
	}

	addr := envOr("ADDR", ":8080")
	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, s.routes())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// sessionKey は 16 進 64 文字（32 バイト）の鍵を読む。未設定なら起動ごとに生成する（再起動でログインが切れる）。
func sessionKey(s string) ([]byte, error) {
	if s == "" {
		log.Print("SESSION_KEY が未設定のため一時的な鍵を生成します。再起動するとログイン状態は失われます")
		key := make([]byte, 32)
		_, err := rand.Read(key)
		return key, err
	}
	key, err := hex.DecodeString(s)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("SESSION_KEY は 16 進 64 文字で指定してください（例: openssl rand -hex 32）")
	}
	return key, nil
}

// loadDict は辞書ファイルを読み込む。拡張子が .gz なら展開する。
func loadDict(path string) (*skk.Dict, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".gz") {
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if b, err = io.ReadAll(z); err != nil {
			return nil, err
		}
	}
	return parseDict(b)
}
