package near

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// DeleteAccount deletes the receiver and sends its balance to the beneficiary.
type DeleteAccount struct{ Beneficiary string }

func (a DeleteAccount) encode(w *borsh) {
	w.u8(7)
	w.str(a.Beneficiary)
}

// Relayer pays gas for other accounts by wrapping their signed delegate
// actions (NEP-366 meta transactions) in its own transactions.
type Relayer struct {
	Payer *Account
	// AllowedReceiver is the only contract a relayed action may call.
	AllowedReceiver string

	mu     sync.Mutex
	nonces map[string]*senderNonce
}

type senderNonce struct {
	mu    sync.Mutex
	nonce uint64
}

// NewRelayer returns a relayer that only relays calls to allowedReceiver.
func NewRelayer(payer *Account, allowedReceiver string) *Relayer {
	return &Relayer{Payer: payer, AllowedReceiver: allowedReceiver, nonces: map[string]*senderNonce{}}
}

// ErrNotRelayable is returned for actions the relayer refuses to pay for.
var ErrNotRelayable = errors.New("only no-deposit calls to the market contract are relayed")

// CheckRelayable enforces the relayer's policy: one receiver, function calls only, no deposit.
func (r *Relayer) CheckRelayable(receiver string, actions []Action) error {
	if receiver != r.AllowedReceiver || len(actions) == 0 {
		return ErrNotRelayable
	}
	for _, a := range actions {
		fc, ok := a.(FunctionCall)
		if !ok || (fc.Deposit != nil && fc.Deposit.Sign() != 0) {
			return ErrNotRelayable
		}
	}
	return nil
}

// Relay signs a delegate action for sender with its key and submits it, paying the gas.
func (r *Relayer) Relay(ctx context.Context, sender string, key KeyPair, receiver string, actions ...Action) (*Outcome, error) {
	if err := r.CheckRelayable(receiver, actions); err != nil {
		return nil, err
	}
	slot := r.slot(sender, key)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if slot.nonce == 0 {
			n, err := r.Payer.client.AccessKeyNonce(ctx, sender, key.Public)
			if err != nil {
				return nil, fmt.Errorf("nonce for %s: %w", sender, err)
			}
			slot.nonce = n
		}
		blk, err := r.Payer.blocks.get(ctx)
		if err != nil {
			return nil, err
		}
		slot.nonce++
		d := DelegateAction{
			SenderID: sender, ReceiverID: receiver, Actions: actions,
			Nonce: slot.nonce, MaxBlockHeight: blk.Height + 300, PublicKey: key.Public,
		}
		out, err := r.Payer.Send(ctx, sender, Delegate{Signed: SignDelegate(d, key)})
		if err != nil {
			lastErr = err
			var te *TxError
			if errors.As(err, &te) && (bytes.Contains([]byte(te.Message), []byte("DelegateActionInvalidNonce")) || bytes.Contains([]byte(te.Message), []byte("DelegateActionExpired"))) {
				slot.nonce = 0
				continue
			}
			if !errors.As(err, &te) {
				slot.nonce = 0
			}
			return out, err
		}
		return out, nil
	}
	return nil, lastErr
}

func (r *Relayer) slot(sender string, key KeyPair) *senderNonce {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := sender + "/" + Base58Encode(key.Public)
	s, ok := r.nonces[id]
	if !ok {
		s = &senderNonce{}
		r.nonces[id] = s
	}
	return s
}

// FaucetCreate asks the testnet helper to create a funded top-level account
// with the given public key. No login is needed.
func FaucetCreate(ctx context.Context, helperURL, accountID string, pub []byte) error {
	body, _ := json.Marshal(map[string]string{
		"newAccountId":        accountID,
		"newAccountPublicKey": "ed25519:" + Base58Encode(pub),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, helperURL+"/account", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("faucet http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	return nil
}
