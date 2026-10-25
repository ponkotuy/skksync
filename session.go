package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

const (
	sessionCookie = "skksync_session"
	stateCookie   = "skksync_oauth_state"
	sessionMaxAge = 30 * 24 * time.Hour
)

// Session は GitHub のアクセストークンを持つ。DB を持たないため AES-GCM で暗号化して Cookie に入れる。
type Session struct {
	Token string `json:"t"`
	Login string `json:"l"`
	// Expiry はトークンの有効期限。期限なしのトークンではゼロ値。
	// リフレッシュトークンは保持せず、期限が切れたら再ログインしてもらう。
	Expiry time.Time `json:"e,omitzero"`
}

type Sealer struct {
	aead cipher.AEAD
}

func NewSealer(key []byte) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (s *Sealer) Open(str string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(str)
	if err != nil {
		return err
	}
	if len(b) < s.aead.NonceSize() {
		return errors.New("sealed value too short")
	}
	plain, err := s.aead.Open(nil, b[:s.aead.NonceSize()], b[s.aead.NonceSize():], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, v)
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	s.setCookie(w, r, name, "", -time.Second)
}

func (s *Server) session(r *http.Request) *Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	var sess Session
	if err := s.sealer.Open(c.Value, &sess); err != nil || sess.Token == "" {
		return nil
	}
	if !sess.Expiry.IsZero() && time.Now().After(sess.Expiry) {
		return nil
	}
	return &sess
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	s.setCookie(w, r, stateCookie, state, 10*time.Minute)
	http.Redirect(w, r, s.oauth.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(stateCookie)
	if err != nil || c.Value == "" || c.Value != r.URL.Query().Get("state") {
		http.Error(w, "OAuth の state が一致しません。もう一度ログインしてください", http.StatusBadRequest)
		return
	}
	s.clearCookie(w, r, stateCookie)
	// 認可画面でキャンセルされた場合はトップに戻す
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	tok, err := s.oauth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("oauth exchange: %v", err)
		http.Error(w, "GitHub からトークンを取得できませんでした", http.StatusBadGateway)
		return
	}
	login, err := s.github.Login(r.Context(), tok.AccessToken)
	if err != nil {
		log.Printf("fetch user: %v", err)
		http.Error(w, "GitHub のユーザー情報を取得できませんでした", http.StatusBadGateway)
		return
	}
	sealed, err := s.sealer.Seal(Session{Token: tok.AccessToken, Login: login, Expiry: tok.Expiry})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxAge := sessionMaxAge
	if !tok.Expiry.IsZero() {
		maxAge = time.Until(tok.Expiry)
	}
	s.setCookie(w, r, sessionCookie, sealed, maxAge)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearCookie(w, r, sessionCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
