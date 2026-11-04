package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"haat/internal/config"
	"haat/internal/near"
)

// The local network is a single near-sandbox node: the same runtime as
// testnet, on this machine, with no accounts or faucet needed.

const sandboxVersion = "2.13.4"

func sandboxPlatform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "Darwin-arm64", nil
	case "linux/amd64":
		return "Linux-x86_64", nil
	case "linux/arm64":
		return "Linux-aarch64", nil
	}
	return "", fmt.Errorf("near-sandbox has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// ensureSandbox downloads the near-sandbox binary once into dir/bin.
func ensureSandbox(ctx context.Context, dir string) (string, error) {
	bin := filepath.Join(dir, "bin", "near-sandbox")
	if p := os.Getenv("NEAR_SANDBOX_BIN_PATH"); p != "" {
		bin = p
	}
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	plat, err := sandboxPlatform()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://s3-us-west-1.amazonaws.com/build.nearprotocol.com/nearcore/%s/%s/near-sandbox.tar.gz", plat, sandboxVersion)
	fmt.Println("downloading near-sandbox", sandboxVersion, "for", plat)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download near-sandbox: http %d", resp.StatusCode)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("near-sandbox binary not found in the archive")
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(h.Name) != "near-sandbox" || h.Typeflag != tar.TypeReg {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			return "", err
		}
		tmp := bin + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		return bin, os.Rename(tmp, bin)
	}
}

// initHome creates the node's home and tunes it for a fast local game.
func initHome(bin, home, rpcPort, p2pPort string) error {
	if _, err := os.Stat(filepath.Join(home, "genesis.json")); err == nil {
		return nil
	}
	out, err := exec.Command(bin, "--home", home, "init").CombinedOutput()
	if err != nil {
		return fmt.Errorf("near-sandbox init: %v: %s", err, out)
	}
	path := filepath.Join(home, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	cfg["rpc"].(map[string]any)["addr"] = "127.0.0.1:" + rpcPort
	cfg["network"].(map[string]any)["addr"] = "127.0.0.1:" + p2pPort
	cfg["consensus"].(map[string]any)["min_block_production_delay"] = map[string]any{"secs": 0, "nanos": 150_000_000}
	cfg["consensus"].(map[string]any)["max_block_production_delay"] = map[string]any{"secs": 0, "nanos": 600_000_000}
	raw, _ = json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path, raw, 0o644)
}

func waitForRPC(ctx context.Context, c *near.Client) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.FinalBlock(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("the local node did not start; see .localnet/node.log")
}

// localnet runs the whole game on a local node: start the node, create the
// accounts, deploy the market, open the shops and serve the game.
func (a *app) localnet(ctx context.Context, sub string) error {
	dir := a.env.Or("HAAT_LOCALNET_DIR", ".localnet")
	home := filepath.Join(dir, "home")
	if sub == "reset" {
		for _, p := range []string{home, filepath.Join(dir, "env"), filepath.Join(dir, "node.log")} {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
		fmt.Println("local network reset")
		return nil
	}
	stop, err := a.startLocalnet(ctx)
	if err != nil {
		return err
	}
	defer stop()
	if err := a.bootstrap(ctx); err != nil {
		return err
	}
	fmt.Println("\nThe haat is open at http://localhost:" + a.env.Or("PORT", "8080") + " (press Ctrl+C to stop)")
	return a.serve(ctx)
}

// startLocalnet starts a local near-sandbox node and creates the root
// account from the node's genesis account. It returns a function that stops
// the node.
func (a *app) startLocalnet(ctx context.Context) (func(), error) {
	dir := a.env.Or("HAAT_LOCALNET_DIR", ".localnet")
	home := filepath.Join(dir, "home")
	port := a.env.Or("HAAT_LOCALNET_RPC_PORT", "3030")
	defaults := map[string]string{
		"NEAR_NETWORK":          "localnet",
		"NEAR_RPC_URL":          "http://127.0.0.1:" + port,
		"NEAR_EXPLORER_URL":     "",
		"HAAT_DAY_MINUTES":      "2",
		"HAAT_FF_PER_HOUR":      "60",
		"HAAT_MARKET_FUNDING":   "10",
		"HAAT_MERCHANT_FUNDING": "5",
	}
	for k, v := range defaults {
		if _, set := os.LookupEnv(k); !set && a.env.Get(k) == "" {
			if err := a.env.Set(k, v); err != nil {
				return nil, err
			}
		}
	}
	// Reuse an optional model key from the main settings file.
	if a.env.Get("GROQ_API_KEY") == "" && a.env.Get("HAAT_AGENT_BRAIN") != "rules" {
		if main, err := config.Load(".env"); err == nil && main.Get("GROQ_API_KEY") != "" {
			_ = a.env.Set("GROQ_API_KEY", main.Get("GROQ_API_KEY"))
		}
	}
	a.rpc = near.NewClient(a.env.Get("NEAR_RPC_URL"))

	bin, err := ensureSandbox(ctx, dir)
	if err != nil {
		return nil, err
	}
	if err := initHome(bin, home, port, a.env.Or("HAAT_LOCALNET_P2P_PORT", "24568")); err != nil {
		return nil, err
	}
	logf, err := os.Create(filepath.Join(dir, "node.log"))
	if err != nil {
		return nil, err
	}
	node := exec.Command(bin, "--home", home, "run")
	node.Stdout, node.Stderr = logf, logf
	if err := node.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	stop := func() {
		_ = node.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = node.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = node.Process.Kill()
		}
		logf.Close()
	}
	fmt.Println("local NEAR node running at", a.env.Get("NEAR_RPC_URL"))
	if err := waitForRPC(ctx, a.rpc); err != nil {
		stop()
		return nil, err
	}
	if a.env.Get("HAAT_ROOT") == "" {
		if err := a.createLocalRoot(ctx, home); err != nil {
			stop()
			return nil, err
		}
	}
	return stop, nil
}

func (a *app) createLocalRoot(ctx context.Context, home string) error {
	raw, err := os.ReadFile(filepath.Join(home, "validator_key.json"))
	if err != nil {
		return err
	}
	var vk struct {
		AccountID string `json:"account_id"`
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal(raw, &vk); err != nil {
		return err
	}
	gk, err := near.ParseSecretKey(vk.SecretKey)
	if err != nil {
		return err
	}
	genesis := near.NewAccount(a.rpc, vk.AccountID, gk)
	k, err := near.GenerateKey()
	if err != nil {
		return err
	}
	id := "haat." + vk.AccountID
	if _, err := genesis.Send(ctx, id, near.CreateAccount{}, near.Transfer{Deposit: near.MustNEAR("1000")}, near.AddKey{PublicKey: k.Public}); err != nil {
		return fmt.Errorf("create %s: %w", id, err)
	}
	if err := a.env.Set("HAAT_ROOT", id); err != nil {
		return err
	}
	return a.env.Set("HAAT_ROOT_KEYS", k.SecretKeyString())
}
