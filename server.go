package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"

	"skksync/skk"
)

const (
	maxUploadBytes = 32 << 20
	maxDiffLines   = 200
)

type Server struct {
	oauth  *oauth2.Config
	sealer *Sealer
	github *GitHub
	// base は除去対象の辞書（SKK-JISYO.L）。起動時に読み込んだものを読み取り専用で共有する。
	base *skk.Dict
	// fallbackToken はログインしていない利用者の EUC-JP ダウンロードに使うトークン（任意）。
	fallbackToken string
	static        fs.FS
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(s.static))
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("GET /callback", s.handleCallback)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/preview", s.handlePreview)
	mux.HandleFunc("POST /api/sync", s.handleSync)
	mux.HandleFunc("GET /dl/euc-jp", s.handleDownloadEUCJP)
	return checkOrigin(mux)
}

// checkOrigin は別オリジンからの POST を拒否する。SameSite=Lax Cookie と合わせた CSRF 対策。
func checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type userError struct {
	status int
	msg    string
}

func (e *userError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &userError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ue *userError
	if errors.As(err, &ue) {
		writeJSON(w, ue.status, map[string]string{"error": ue.msg})
		return
	}
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "サーバ内部でエラーが発生しました"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	login := ""
	if sess := s.session(r); sess != nil {
		login = sess.Login
	}
	writeJSON(w, http.StatusOK, map[string]string{"login": login})
}

func (s *Server) fetchGistFile(r *http.Request, token, gist, filename string, writing bool) (*GistFile, error) {
	id, err := ParseGistID(gist)
	if err != nil {
		return nil, badRequest("%s", err)
	}
	f, err := s.github.FetchGistFile(r.Context(), token, id, filename)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status >= 500 {
			return nil, err
		}
		return nil, &userError{status: http.StatusBadGateway, msg: describeGitHubError(err, writing)}
	}
	return f, nil
}

type mergeResult struct {
	file         *GistFile
	gistText     string
	gistDict     *skk.Dict
	merged       *skk.Dict
	localEntries int
	removedByL   int
}

// merge はアップロードされた手元の辞書と Gist の辞書をマージし、SKK-JISYO.L にある候補を除く。
// 候補順は手元の辞書（アップロード順）→ Gist の順に優先する。
func (s *Server) merge(w http.ResponseWriter, r *http.Request, token string, writing bool) (*mergeResult, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return nil, badRequest("アップロードを読み込めません: %s", err)
	}
	uploads := r.MultipartForm.File["dict"]
	if len(uploads) == 0 {
		return nil, badRequest("手元の辞書ファイルを指定してください")
	}

	merged := skk.New()
	local := skk.New()
	for _, fh := range uploads {
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		d, err := parseDict(b)
		if err != nil {
			return nil, badRequest("%s: %s", fh.Filename, err)
		}
		local.Merge(d)
	}
	merged.Merge(local)

	file, err := s.fetchGistFile(r, token, r.FormValue("gist"), r.FormValue("file"), writing)
	if err != nil {
		return nil, err
	}
	gistText, err := skk.Decode(file.Content)
	if err != nil {
		return nil, badRequest("Gist の辞書: %s", err)
	}
	gistDict, err := skk.Parse(gistText)
	if err != nil {
		return nil, badRequest("Gist の辞書: %s", err)
	}
	merged.Merge(gistDict)
	removed := merged.Subtract(s.base)

	return &mergeResult{
		file:         file,
		gistText:     gistText,
		gistDict:     gistDict,
		merged:       merged,
		localEntries: local.Len(),
		removedByL:   removed,
	}, nil
}

func parseDict(b []byte) (*skk.Dict, error) {
	text, err := skk.Decode(b)
	if err != nil {
		return nil, err
	}
	return skk.Parse(text)
}

type mergeResponse struct {
	Gist struct {
		ID      string `json:"id"`
		File    string `json:"file"`
		HTMLURL string `json:"html_url"`
		RawURL  string `json:"raw_url"`
	} `json:"gist"`
	Counts struct {
		Gist       int `json:"gist"`
		Local      int `json:"local"`
		Merged     int `json:"merged"`
		RemovedByL int `json:"removed_by_l"`
		Added      int `json:"added"`
		Changed    int `json:"changed"`
		Removed    int `json:"removed"`
	} `json:"counts"`
	Diff skk.Diff `json:"diff"`
	// Unchanged は Gist の内容が書き出し結果と完全に一致し、更新の必要がないことを表す。
	Unchanged bool `json:"unchanged"`
	Updated   bool `json:"updated"`
}

func (m *mergeResult) response() *mergeResponse {
	var res mergeResponse
	res.Gist.ID = m.file.GistID
	res.Gist.File = m.file.Filename
	res.Gist.HTMLURL = m.file.HTMLURL
	res.Gist.RawURL = m.file.LatestRawURL()

	diff := skk.Compare(m.gistDict, m.merged)
	res.Counts.Gist = m.gistDict.Len()
	res.Counts.Local = m.localEntries
	res.Counts.Merged = m.merged.Len()
	res.Counts.RemovedByL = m.removedByL
	res.Counts.Added = len(diff.Added)
	res.Counts.Changed = len(diff.Changed)
	res.Counts.Removed = len(diff.Removed)
	res.Diff = skk.Diff{
		Added:   head(diff.Added, maxDiffLines),
		Changed: head(diff.Changed, maxDiffLines),
		Removed: head(diff.Removed, maxDiffLines),
	}
	res.Unchanged = m.merged.Format() == m.gistText
	return &res
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	token := s.fallbackToken
	if sess := s.session(r); sess != nil {
		token = sess.Token
	}
	m, err := s.merge(w, r, token, false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m.response())
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r)
	if sess == nil {
		writeError(w, &userError{status: http.StatusUnauthorized, msg: "GitHub にログインしてください"})
		return
	}
	m, err := s.merge(w, r, sess.Token, true)
	if err != nil {
		writeError(w, err)
		return
	}
	res := m.response()
	if !res.Unchanged {
		err := s.github.UpdateGistFile(r.Context(), sess.Token, m.file.GistID, m.file.Filename, m.merged.Format())
		if err != nil {
			writeError(w, &userError{status: http.StatusBadGateway, msg: describeGitHubError(err, true)})
			return
		}
		res.Updated = true
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDownloadEUCJP は Gist の辞書を EUC-JP に変換して返す。
// URL が固定なので、EUC-JP しか読めないクライアント向けに curl 等で定期取得する用途も想定している。
func (s *Server) handleDownloadEUCJP(w http.ResponseWriter, r *http.Request) {
	token := s.fallbackToken
	if sess := s.session(r); sess != nil {
		token = sess.Token
	}
	q := r.URL.Query()
	file, err := s.fetchGistFile(r, token, q.Get("gist"), q.Get("file"), false)
	if err != nil {
		writeTextError(w, err)
		return
	}
	d, err := parseDict(file.Content)
	if err != nil {
		writeTextError(w, badRequest("Gist の辞書: %s", err))
		return
	}
	out, dropped, err := skk.EncodeEUCJP(d)
	if err != nil {
		writeTextError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=EUC-JP")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	w.Header().Set("X-SKKSync-Dropped-Candidates", fmt.Sprint(dropped))
	_, _ = w.Write(out)
}

func writeTextError(w http.ResponseWriter, err error) {
	var ue *userError
	if errors.As(err, &ue) {
		http.Error(w, ue.msg, ue.status)
		return
	}
	log.Printf("internal error: %v", err)
	http.Error(w, "サーバ内部でエラーが発生しました", http.StatusInternalServerError)
}
