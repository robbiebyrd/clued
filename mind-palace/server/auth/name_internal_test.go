package auth

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"
)

// A suffix already taken is drawn again: the random source here repeats the
// first exchange's suffix once before giving a new one.
func TestExchangeDrawsAnotherSuffixWhenTheNameIsTaken(t *testing.T) {
	draws := [][]byte{{0xde, 0xad, 0xbe, 0xef}, {0xde, 0xad, 0xbe, 0xef}, {0x01, 0x02, 0x03, 0x04}}
	n, saved := 0, nameRand
	nameRand = func(b []byte) (int, error) {
		copy(b, draws[n])
		n++
		return len(b), nil
	}
	t.Cleanup(func() { nameRand = saved })

	f := OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	key, err := f.CreateEnvironmentKey("k", "acct", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.Exchange(context.Background(), key, "box", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.Exchange(context.Background(), key, "box", nil)
	if err != nil {
		t.Fatalf("the second exchange failed on a taken name: %v", err)
	}
	if first.Name != "k@box-deadbeef" || second.Name != "k@box-01020304" || n != 3 {
		t.Errorf("names %q, %q after %d draws", first.Name, second.Name, n)
	}
}

func TestExchangedNamesCarryEightHexDigits(t *testing.T) {
	f := OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	key, err := f.CreateEnvironmentKey("k", "acct", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Exchange(context.Background(), key, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^k@[0-9a-f]{8}$`).MatchString(got.Name) {
		t.Errorf("name = %q", got.Name)
	}
}
