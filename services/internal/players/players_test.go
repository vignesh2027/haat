package players

import (
	"regexp"
	"testing"
)

func TestNames(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[0-9a-f]{4}$`)
	for i := 0; i < 50; i++ {
		if n := RandomName(); !re.MatchString(n) {
			t.Fatalf("bad handle %q", n)
		}
	}
	if DisplayName("kavya-3f9a.haat-1.testnet") != "Kavya" {
		t.Fatal("display name")
	}
}
