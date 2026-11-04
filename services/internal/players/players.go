// Package players creates player accounts and plays on their behalf through
// the gas relayer, so a player never sees a key, a wallet or a gas fee.
package players

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"haat/internal/market"
	"haat/internal/near"
)

// Player is a player account and its two keys. The recovery key has full
// access; the play key can only call the market contract.
type Player struct {
	AccountID string
	Name      string
	Recovery  near.KeyPair
	Play      near.KeyPair
}

// Funding is the NEAR each new player account gets for its own storage.
var Funding = near.MustNEAR("0.01")

var firstNames = []string{
	"kavya", "arjun", "meera", "rohan", "ananya", "vikram", "priya", "karthik", "divya", "aditya",
	"lakshmi", "suresh", "nisha", "rahul", "pooja", "manoj", "sneha", "ravi", "isha", "ganesh",
	"farah", "imran", "zoya", "harpreet", "tenzin", "anjali", "deepak", "keerthi", "naveen", "sana",
}

// RandomName returns a short village-style handle such as "kavya-3f9a".
func RandomName() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(firstNames))))
	return fmt.Sprintf("%s-%s", firstNames[n.Int64()], hex.EncodeToString(b[:])[:4])
}

// DisplayName turns "kavya-3f9a.root.testnet" into "Kavya".
func DisplayName(accountID string) string {
	head, _, _ := strings.Cut(accountID, ".")
	head, _, _ = strings.Cut(head, "-")
	if head == "" {
		return "Villager"
	}
	return strings.ToUpper(head[:1]) + head[1:]
}

// Create makes a new player account under root in one transaction, then
// registers it with the market for the starter purse.
func Create(ctx context.Context, root *near.Account, marketID, handle string) (*Player, error) {
	rec, err := near.GenerateKey()
	if err != nil {
		return nil, err
	}
	play, err := near.GenerateKey()
	if err != nil {
		return nil, err
	}
	id := handle + "." + root.ID
	_, err = root.Send(ctx, id,
		near.CreateAccount{},
		near.Transfer{Deposit: Funding},
		near.AddKey{PublicKey: rec.Public},
		near.AddKey{PublicKey: play.Public, Permission: &near.FunctionCallPermission{ReceiverID: marketID}},
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", id, err)
	}
	if _, err := root.Send(ctx, marketID, market.RegisterPlayer(id)); err != nil {
		return nil, fmt.Errorf("register %s: %w", id, err)
	}
	return &Player{AccountID: id, Name: DisplayName(id), Recovery: rec, Play: play}, nil
}

// Act sends game actions for a player through the relayer. The relayer pays the gas.
func Act(ctx context.Context, r *near.Relayer, p *Player, actions ...near.Action) (*near.Outcome, error) {
	return r.Relay(ctx, p.AccountID, p.Play, r.AllowedReceiver, actions...)
}
