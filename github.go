package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// GitHub は Gist API の必要な部分だけを扱うクライアント。
type GitHub struct {
	HTTP    *http.Client
	APIBase string
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API: %d %s", e.Status, e.Message)
}

type GistFile struct {
	GistID   string
	Filename string
	Owner    string
	HTMLURL  string
	Content  []byte
}

// LatestRawURL は常に最新リビジョンを指す raw URL を返す。
func (f *GistFile) LatestRawURL() string {
	if f.Owner == "" {
		return ""
	}
	return fmt.Sprintf("https://gist.githubusercontent.com/%s/%s/raw/%s", f.Owner, f.GistID, url.PathEscape(f.Filename))
}

var gistIDRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// ParseGistID は Gist の URL（gist.github.com / gist.githubusercontent.com）か ID そのものから ID を取り出す。
func ParseGistID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if gistIDRe.MatchString(s) {
		return s, nil
	}
	u, err := url.Parse(s)
	if err == nil && (u.Host == "gist.github.com" || u.Host == "gist.githubusercontent.com") {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		// /{id} または /{user}/{id}/...
		for _, i := range []int{1, 0} {
			if i < len(segs) && gistIDRe.MatchString(segs[i]) {
				return segs[i], nil
			}
		}
	}
	return "", fmt.Errorf("Gist の URL として認識できません: %s", s)
}

func (g *GitHub) do(ctx context.Context, method, path, token string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.APIBase+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "skksync")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		return &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// FetchGistFile は Gist 内のファイルを 1 つ取得する。filename が空ならファイルが 1 つだけの Gist を想定する。
// API レスポンスの content は 1MB で切り詰められ、UTF-8 以外も正しく扱えないため、本文は raw_url から取る。
func (g *GitHub) FetchGistFile(ctx context.Context, token, id, filename string) (*GistFile, error) {
	var gist struct {
		HTMLURL string `json:"html_url"`
		Owner   *struct {
			Login string `json:"login"`
		} `json:"owner"`
		Files map[string]struct {
			RawURL string `json:"raw_url"`
		} `json:"files"`
	}
	if err := g.do(ctx, http.MethodGet, "/gists/"+id, token, nil, &gist); err != nil {
		return nil, err
	}
	if filename == "" {
		if len(gist.Files) != 1 {
			names := make([]string, 0, len(gist.Files))
			for n := range gist.Files {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("Gist に複数のファイルがあります。ファイル名を指定してください: %s", strings.Join(names, ", "))
		}
		for n := range gist.Files {
			filename = n
		}
	}
	meta, ok := gist.Files[filename]
	if !ok {
		return nil, fmt.Errorf("Gist にファイル %q がありません", filename)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.RawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "skksync")
	res, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gist のファイル取得に失敗しました: %s", res.Status)
	}
	content, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	f := &GistFile{GistID: id, Filename: filename, HTMLURL: gist.HTMLURL, Content: content}
	if gist.Owner != nil {
		f.Owner = gist.Owner.Login
	}
	return f, nil
}

func (g *GitHub) UpdateGistFile(ctx context.Context, token, id, filename, content string) error {
	body := map[string]any{
		"files": map[string]any{filename: map[string]string{"content": content}},
	}
	return g.do(ctx, http.MethodPatch, "/gists/"+id, token, body, nil)
}

func (g *GitHub) Login(ctx context.Context, token string) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if err := g.do(ctx, http.MethodGet, "/user", token, nil, &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// describeGitHubError は API エラーを利用者向けの文言にする。
func describeGitHubError(err error, writing bool) string {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err.Error()
	}
	switch {
	case ae.Status == http.StatusNotFound && writing:
		return "Gist を更新できません。自分の Gist か確認してください"
	case ae.Status == http.StatusNotFound:
		return "Gist が見つかりません"
	case ae.Status == http.StatusUnauthorized:
		return "GitHub の認証が切れています。再ログインしてください"
	case ae.Status == http.StatusForbidden || ae.Status == http.StatusTooManyRequests:
		return "GitHub API に拒否されました（レート制限の可能性があります）: " + ae.Message
	}
	return err.Error()
}
