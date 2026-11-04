package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Voice rewrites a shopkeeper's plain note in their own voice using a free
// open model on Groq. It is flavour only: it never sets a number, and when no
// key is configured or the call fails, the plain note is used instead.
type Voice struct {
	APIKey string
	Model  string
	URL    string
	HTTP   *http.Client

	mu    sync.Mutex
	cache map[string]string
	busy  map[string]bool
}

// NewVoice returns a voice. An empty key disables it.
func NewVoice(apiKey string) *Voice {
	return &Voice{
		APIKey: apiKey,
		Model:  "openai/gpt-oss-20b",
		URL:    "https://api.groq.com/openai/v1/chat/completions",
		HTTP:   &http.Client{Timeout: 8 * time.Second},
		cache:  map[string]string{},
		busy:   map[string]bool{},
	}
}

// Enabled reports whether a model is configured.
func (v *Voice) Enabled() bool { return v != nil && v.APIKey != "" }

// Line returns the voiced line for a note if it is ready, otherwise "" and
// starts generating it in the background. Pages never wait for the model.
func (v *Voice) Line(spec Spec, note string) string {
	if !v.Enabled() || note == "" {
		return ""
	}
	key := spec.Key + "|" + note
	v.mu.Lock()
	defer v.mu.Unlock()
	if s, ok := v.cache[key]; ok {
		return s
	}
	if !v.busy[key] {
		v.busy[key] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := v.say(ctx, spec, note)
			v.mu.Lock()
			defer v.mu.Unlock()
			delete(v.busy, key)
			if err == nil {
				v.cache[key] = s
			}
		}()
	}
	return ""
}

func (v *Voice) say(ctx context.Context, spec Spec, note string) (string, error) {
	system := fmt.Sprintf("You are %s, a %s shopkeeper at a village market (haat) in India. "+
		"Rewrite the shopkeeper's note as one short spoken sentence in their voice, warm and specific, at most 25 words. "+
		"Plain English with at most one Hindi word. Keep every fact and number exactly as given, add no new numbers, no quotes, no emoji, no dashes.",
		spec.Name, spec.Temperament)
	body, _ := json.Marshal(map[string]any{
		"model":            v.Model,
		"temperature":      0.6,
		"max_tokens":       300,
		"reasoning_effort": "low",
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": note},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+v.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq http %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("groq: empty reply")
	}
	return Clean(out.Choices[0].Message.Content), nil
}

// Clean keeps model output inside the house style: one line, no quotes,
// no dashes used as punctuation, no arrows.
func Clean(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	s = strings.Trim(s, "\"'“”")
	for _, bad := range []string{"—", "–", " - ", "->", "→"} {
		s = strings.ReplaceAll(s, bad, ", ")
	}
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, ",,", ",")
	if len(s) > 220 {
		s = s[:220]
	}
	return s
}
