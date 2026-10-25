package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"

	"skksync/skk"
)

// fakeGitHub は Gist API と raw ファイルの配信を模倣する。
type fakeGitHub struct {
	content string
	patched string
	srv     *httptest.Server
}

func newFakeGitHub(t *testing.T, content string) *fakeGitHub {
	f := &fakeGitHub{content: content}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gists/abc123", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://gist.github.com/abc123",
			"owner":    map[string]string{"login": "alice"},
			"files": map[string]any{
				"SKK-JISYO.user": map[string]string{"raw_url": f.srv.URL + "/raw/SKK-JISYO.user"},
			},
		})
	})
	mux.HandleFunc("GET /raw/SKK-JISYO.user", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, f.content)
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"login":"alice"}`)
	})
	mux.HandleFunc("PATCH /gists/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Files map[string]struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.patched = body.Files["SKK-JISYO.user"].Content
		f.content = f.patched
		w.Write([]byte("{}"))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func newTestServer(t *testing.T, gh *fakeGitHub) *Server {
	t.Helper()
	sealer, err := NewSealer(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	base, err := skk.Parse(";; okuri-nasi entries.\nかんじ /漢字/感じ/\n")
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		sealer: sealer,
		github: &GitHub{HTTP: gh.srv.Client(), APIBase: gh.srv.URL},
		base:   base,
	}
}

func multipartRequest(t *testing.T, path string, dicts map[string][]byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("gist", "https://gist.github.com/alice/abc123")
	for name, content := range dicts {
		fw, _ := mw.CreateFormFile("dict", name)
		fw.Write(content)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestSyncFlow(t *testing.T) {
	gh := newFakeGitHub(t, ";; okuri-nasi entries.\nほげ /保下/\n")
	s := newTestServer(t, gh)
	h := s.routes()

	local, _ := japanese.EUCJP.NewEncoder().String("かんじ /幹事/漢字/\nあb /亜/\n")
	dicts := map[string][]byte{"local.jisyo": []byte(local)}

	// 未ログインでも preview はできる
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, multipartRequest(t, "/api/preview", dicts))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var res mergeResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Counts.Added != 2 || res.Counts.RemovedByL != 1 || res.Gist.RawURL != "https://gist.githubusercontent.com/alice/abc123/raw/SKK-JISYO.user" {
		t.Errorf("unexpected preview: %+v", res)
	}

	// 未ログインでは sync できない
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, multipartRequest(t, "/api/sync", dicts))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sync without login: %d", rec.Code)
	}

	cookie, _ := s.sealer.Seal(Session{Token: "secret-token", Login: "alice"})
	req := multipartRequest(t, "/api/sync", dicts)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", rec.Code, rec.Body)
	}
	want := strings.Join([]string{
		";; -*- mode: fundamental; coding: utf-8 -*-",
		";; okuri-ari entries.",
		"あb /亜/",
		";; okuri-nasi entries.",
		"かんじ /幹事/",
		"ほげ /保下/",
		"",
	}, "\n")
	if gh.patched != want {
		t.Errorf("patched content:\n%s\nwant:\n%s", gh.patched, want)
	}

	// 同じ内容で再度 sync しても更新しない
	gh.patched = ""
	req = multipartRequest(t, "/api/sync", dicts)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.Unchanged || res.Updated || gh.patched != "" {
		t.Errorf("expected no update: %+v", res)
	}

	// EUC-JP でダウンロードできる
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dl/euc-jp?gist=abc123", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	got, _ := japanese.EUCJP.NewDecoder().String(rec.Body.String())
	if got != strings.Replace(want, "utf-8", "euc-jp", 1) {
		t.Errorf("download content:\n%s", got)
	}
}

func TestExpiredSession(t *testing.T) {
	s := newTestServer(t, newFakeGitHub(t, ""))
	for _, c := range []struct {
		expiry time.Time
		valid  bool
	}{
		{time.Time{}, true},
		{time.Now().Add(time.Hour), true},
		{time.Now().Add(-time.Minute), false},
	} {
		cookie, _ := s.sealer.Seal(Session{Token: "t", Login: "alice", Expiry: c.expiry})
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		if got := s.session(req) != nil; got != c.valid {
			t.Errorf("expiry %v: valid = %v, want %v", c.expiry, got, c.valid)
		}
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	gh := newFakeGitHub(t, "")
	h := newTestServer(t, gh).routes()
	req := multipartRequest(t, "/api/preview", map[string][]byte{"a": []byte("")})
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestParseGistID(t *testing.T) {
	cases := map[string]string{
		"0123abcd":                                                      "0123abcd",
		"https://gist.github.com/alice/0123abcd":                        "0123abcd",
		"https://gist.github.com/0123abcd":                              "0123abcd",
		"https://gist.github.com/alice/0123abcd/#file-skk-jisyo":        "0123abcd",
		"https://gist.github.com/abc/0123abcd":                          "0123abcd",
		"https://gist.githubusercontent.com/alice/0123abcd/raw/a/b.txt": "0123abcd",
	}
	for in, want := range cases {
		if got, err := ParseGistID(in); err != nil || got != want {
			t.Errorf("ParseGistID(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseGistID("https://example.com/alice/0123abcd"); err == nil {
		t.Error("expected error for non-gist host")
	}
}
