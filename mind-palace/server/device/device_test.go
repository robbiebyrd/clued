package device

import (
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
)

// fakeTokens records the tokens minted and revoked; names are unique like
// the token file's.
type fakeTokens struct {
	created []minted
	revoked []string
	fail    error
}

type minted struct {
	name, account string
	projects      []string
}

func (f *fakeTokens) Create(name, account string, projects []string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	for _, m := range f.created {
		if m.name == name {
			return "", fmt.Errorf("%w: %q", auth.ErrTokenExists, name)
		}
	}
	f.created = append(f.created, minted{name, account, projects})
	return "raw-" + name, nil
}

func (f *fakeTokens) Revoke(name string) error {
	f.revoked = append(f.revoked, name)
	return nil
}

// clock is a settable Now.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newFlow(t *testing.T) (*Flow, *fakeTokens, *clock) {
	t.Helper()
	tokens := &fakeTokens{}
	c := &clock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	return New(Options{Tokens: tokens, Now: c.now}), tokens, c
}

var (
	star = auth.Principal{Name: "star", AccountID: "acct-star", Projects: []string{"*"}}
	ab   = auth.Principal{Name: "ab", AccountID: "acct-ab", Projects: []string{"a", "b"}}
)

func TestStartPollApproveHandsTheTokenOverOnce(t *testing.T) {
	f, tokens, c := newFlow(t)
	st, err := f.Start("laptop", map[string]string{"os": "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[BCDFGHJKMNPQRSTVWXZ2-9]{4}-[BCDFGHJKMNPQRSTVWXZ2-9]{4}$`).MatchString(st.UserCode) {
		t.Errorf("user code %q", st.UserCode)
	}
	if st.DeviceCode == "" || st.ExpiresIn != 900 || st.Interval != 5 {
		t.Errorf("start = %+v", st)
	}
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrPending) {
		t.Errorf("first poll: %v, want pending", err)
	}
	c.t = c.t.Add(time.Second)
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrSlowDown) {
		t.Errorf("poll after 1s: %v, want slow_down", err)
	}
	c.t = c.t.Add(5 * time.Second)
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrPending) {
		t.Errorf("poll after the interval: %v, want pending", err)
	}
	pending := f.Pending()
	if len(pending) != 1 || pending[0].UserCode != st.UserCode || pending[0].ClientName != "laptop" || pending[0].ClientInfo["os"] != "darwin" || pending[0].State != "pending" {
		t.Errorf("pending = %+v", pending)
	}

	// Approval takes the approver's account and projects and names the
	// token after the client; the code may be given in any formatting.
	got, err := f.Approve(" bcdf-ghjk ", star, "", nil)
	if !errors.Is(err, ErrNoRequest) {
		t.Errorf("approving an unknown code: %+v %v", got, err)
	}
	got, err = f.Approve(lowerNoDash(st.UserCode), star, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Approval{UserCode: st.UserCode, Name: "laptop", AccountID: "acct-star", Projects: []string{"*"}}
	if got.UserCode != want.UserCode || got.Name != want.Name || got.AccountID != want.AccountID || len(got.Projects) != 1 || got.Projects[0] != "*" {
		t.Errorf("approval = %+v, want %+v", got, want)
	}
	if len(tokens.created) != 1 || tokens.created[0].name != "laptop" || tokens.created[0].account != "acct-star" {
		t.Errorf("minted = %+v", tokens.created)
	}
	if _, err := f.Approve(st.UserCode, star, "", nil); !errors.Is(err, ErrDecided) {
		t.Errorf("approving twice: %v", err)
	}
	if f.Pending()[0].State != "approved" {
		t.Errorf("state after approval = %q", f.Pending()[0].State)
	}

	c.t = c.t.Add(5 * time.Second)
	link, err := f.Poll(st.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if link != (Link{Token: "raw-laptop", TokenID: "laptop", Name: "laptop", AccountID: "acct-star"}) {
		t.Errorf("link = %+v", link)
	}
	c.t = c.t.Add(5 * time.Second)
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrUnknown) {
		t.Errorf("poll after the hand-over: %v, want invalid_grant", err)
	}
	if len(f.Pending()) != 0 {
		t.Errorf("the request should be gone: %+v", f.Pending())
	}
}

func TestApproveNamesAndProjects(t *testing.T) {
	f, tokens, _ := newFlow(t)
	tokens.created = []minted{{name: "laptop"}, {name: "laptop-2"}}

	st, _ := f.Start("laptop", nil)
	got, err := f.Approve(st.UserCode, ab, "", []string{"a", " b ", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "laptop-3" || len(got.Projects) != 2 || got.Projects[0] != "a" || got.Projects[1] != "b" {
		t.Errorf("approval = %+v", got)
	}

	st, _ = f.Start("", nil)
	if _, err := f.Approve(st.UserCode, ab, "", []string{"a", "c"}); !errors.Is(err, ErrProject) {
		t.Errorf("project outside the approver's: %v", err)
	}
	if _, err := f.Approve(st.UserCode, ab, "", []string{"*"}); !errors.Is(err, ErrProject) {
		t.Errorf("'*' from a token without it: %v", err)
	}
	got, err = f.Approve(st.UserCode, star, "ci", []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ci" || got.AccountID != "acct-star" || len(got.Projects) != 1 || got.Projects[0] != "x" {
		t.Errorf("approval = %+v", got)
	}
	if f.Pending()[1].ClientName != DefaultClientName {
		t.Errorf("empty client name should become %q: %+v", DefaultClientName, f.Pending())
	}

	tokens.fail = errors.New("disk full")
	st, _ = f.Start("desk", nil)
	if _, err := f.Approve(st.UserCode, star, "", nil); err == nil || errors.Is(err, auth.ErrTokenExists) {
		t.Errorf("a token file failure should surface: %v", err)
	}
	if f.Pending()[2].State != "pending" {
		t.Error("a failed approval must leave the request pending")
	}
}

func TestDenyExpiryAndLimits(t *testing.T) {
	f, _, c := newFlow(t)
	st, _ := f.Start("laptop", nil)
	if err := f.Deny("nope"); !errors.Is(err, ErrNoRequest) {
		t.Errorf("deny unknown: %v", err)
	}
	if err := f.Deny(st.UserCode); err != nil {
		t.Fatal(err)
	}
	if err := f.Deny(st.UserCode); !errors.Is(err, ErrDecided) {
		t.Errorf("deny twice: %v", err)
	}
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrDenied) {
		t.Errorf("poll after deny: %v", err)
	}
	c.t = c.t.Add(5 * time.Second)
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrUnknown) {
		t.Errorf("a denied request is forgotten: %v", err)
	}

	st, _ = f.Start("laptop", nil)
	c.t = c.t.Add(DefaultTTL)
	if _, err := f.Poll(st.DeviceCode); !errors.Is(err, ErrExpired) {
		t.Errorf("poll after the TTL: %v", err)
	}
	st, _ = f.Start("laptop", nil)
	c.t = c.t.Add(DefaultTTL)
	if _, err := f.Approve(st.UserCode, star, "", nil); !errors.Is(err, ErrExpired) {
		t.Errorf("approve after the TTL: %v", err)
	}
	if len(f.Pending()) != 0 {
		t.Errorf("expired requests should be pruned: %+v", f.Pending())
	}

	for i := 0; i < MaxPending; i++ {
		if _, err := f.Start("many", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Start("one too many", nil); !errors.Is(err, ErrTooMany) {
		t.Errorf("start above MaxPending: %v", err)
	}
	c.t = c.t.Add(DefaultTTL)
	if _, err := f.Start("after expiry", nil); err != nil {
		t.Errorf("start once the old ones expired: %v", err)
	}
}

func TestNormalizeAndFormat(t *testing.T) {
	if got := Normalize(" bcdf-ghjk\n"); got != "BCDFGHJK" {
		t.Errorf("Normalize = %q", got)
	}
	if got := Format("BCDFGHJK"); got != "BCDF-GHJK" {
		t.Errorf("Format = %q", got)
	}
	if got := Format("SHORT"); got != "SHORT" {
		t.Errorf("Format of an odd code = %q", got)
	}
	f, tokens, _ := newFlow(t)
	if err := f.RevokeToken("laptop"); err != nil || len(tokens.revoked) != 1 || tokens.revoked[0] != "laptop" {
		t.Errorf("RevokeToken: %v %v", err, tokens.revoked)
	}
}

func lowerNoDash(code string) string {
	out := []byte{}
	for i := 0; i < len(code); i++ {
		if code[i] == '-' {
			out = append(out, ' ')
			continue
		}
		c := code[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
