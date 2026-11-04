package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"haat/internal/near"
)

// Session is what the player's browser carries. It holds the player's keys,
// sealed with AES-GCM so only this server can read them and nobody can alter
// them. The server stores no copy: lose the cookie and the keys are gone.
type Session struct {
	Account  string `json:"a"`
	Play     string `json:"p"`           // function-call key, can only call the market contract
	Recovery string `json:"r,omitempty"` // full-access key, removed once shown to the player
}

const cookieName = "haat_pass"

// Sealer encrypts and decrypts sessions.
type Sealer struct{ aead cipher.AEAD }

// NewSealer takes a 32-byte key as 64 hex characters.
func NewSealer(hexKey string) (*Sealer, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("HAAT_COOKIE_SECRET must be 64 hex characters")
	}
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

// Seal encrypts a session into a cookie-safe string.
func (s *Sealer) Seal(sess Session) (string, error) {
	plain, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plain, []byte(cookieName))
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a sealed session.
func (s *Sealer) Open(token string) (Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return Session{}, errors.New("bad pass")
	}
	plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], []byte(cookieName))
	if err != nil {
		return Session{}, errors.New("bad pass")
	}
	var sess Session
	if err := json.Unmarshal(plain, &sess); err != nil || sess.Account == "" {
		return Session{}, errors.New("bad pass")
	}
	return sess, nil
}

// PlayKey parses the play key.
func (s Session) PlayKey() (near.KeyPair, error) { return near.ParseSecretKey(s.Play) }

func (srv *Server) readSession(r *http.Request) (Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, false
	}
	sess, err := srv.sealer.Open(c.Value)
	return sess, err == nil
}

func (srv *Server) writeSession(w http.ResponseWriter, r *http.Request, sess Session) error {
	v, err := srv.sealer.Seal(sess)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: v, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
		Expires: time.Now().AddDate(1, 0, 0),
	})
	return nil
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// Flash messages survive one redirect.
const flashCookie = "haat_flash"

type flash struct {
	Kind string `json:"k"` // ok or err
	Text string `json:"t"`
}

func setFlash(w http.ResponseWriter, kind, text string) {
	b, _ := json.Marshal(flash{Kind: kind, Text: text})
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60})
}

func takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	var f flash
	if json.Unmarshal(raw, &f) != nil || f.Text == "" {
		return nil
	}
	return &f
}
