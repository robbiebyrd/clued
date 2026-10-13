// Package device implements the device-code login of the clued server. A
// client without a token asks for a code (Start), shows the user code to its
// user and polls for the outcome (Poll). Someone who already holds a token
// for the server approves the code (Approve), which mints a token for the
// client in the token file under the approver's account, and the next poll
// hands that token over, exactly once. Requests live in memory and expire
// after TTL; a denied request (Deny) is reported to the client on its next
// poll and then forgotten.
//
// The transport (server/device_routes.go) maps the sentinel errors to the
// error kinds of the device-flow convention the client expects:
// authorization_pending, slow_down, access_denied, expired_token and
// invalid_grant.
package device

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
)

// Tokens mints and revokes tokens; *auth.TokenFile implements it.
type Tokens interface {
	Create(name, accountID string, projects []string) (string, error)
	Revoke(name string) error
}

const (
	// DefaultTTL is how long a request waits for its approval.
	DefaultTTL = 15 * time.Minute
	// DefaultInterval is the polling interval told to the client; polling
	// much faster than that answers slow_down.
	DefaultInterval = 5 * time.Second
	// MaxPending bounds the requests kept in memory; Start refuses more.
	MaxPending = 100
	// DefaultClientName names a client that gave none.
	DefaultClientName = "clued client"

	// userCodeAlphabet leaves out vowels and look-alikes (0/O, 1/I/L).
	userCodeAlphabet = "BCDFGHJKMNPQRSTVWXZ23456789"
	userCodeLen      = 8
	maxNameTries     = 50
	maxClientName    = 80
)

// Outcomes of Poll, named after the error kinds of the device flow.
var (
	ErrPending  = errors.New("authorization_pending")
	ErrSlowDown = errors.New("slow_down")
	ErrDenied   = errors.New("access_denied")
	ErrExpired  = errors.New("expired_token")
	ErrUnknown  = errors.New("invalid_grant")
)

// Failures of Start, Approve and Deny.
var (
	ErrTooMany   = errors.New("device: too many pending requests")
	ErrNoRequest = errors.New("device: no request with that code")
	ErrDecided   = errors.New("device: the request was already approved or denied")
	ErrProject   = errors.New("device: the approver's token does not cover that project")
)

// Options configures a Flow. Tokens is required; the rest default.
type Options struct {
	Tokens   Tokens
	TTL      time.Duration
	Interval time.Duration
	Now      func() time.Time
}

// Start is what a client receives when it asks for a code.
type Start struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

// Link is what an approved client receives: its token, shown once.
type Link struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

// Request describes a request to whoever may approve it.
type Request struct {
	UserCode   string            `json:"user_code"`
	ClientName string            `json:"client_name"`
	ClientInfo map[string]string `json:"client_info,omitempty"`
	Created    time.Time         `json:"created"`
	Expires    time.Time         `json:"expires"`
	State      string            `json:"state"`
}

// Approval is what Approve reports: the token minted for the client.
type Approval struct {
	UserCode  string   `json:"user_code"`
	Name      string   `json:"name"`
	AccountID string   `json:"account_id"`
	Projects  []string `json:"projects"`
}

type state int

const (
	pending state = iota
	approved
	denied
)

func (s state) String() string {
	switch s {
	case approved:
		return "approved"
	case denied:
		return "denied"
	}
	return "pending"
}

type request struct {
	deviceCode, userCode, clientName string
	info                             map[string]string
	created, expires, lastPoll       time.Time
	state                            state
	link                             Link
}

// Flow holds the requests of one server.
type Flow struct {
	opts Options

	mu       sync.Mutex
	byDevice map[string]*request
	byUser   map[string]*request
}

// New returns a Flow. It panics without Tokens.
func New(opts Options) *Flow {
	if opts.Tokens == nil {
		panic("device: Options.Tokens is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Flow{opts: opts, byDevice: map[string]*request{}, byUser: map[string]*request{}}
}

// Start registers a request for clientName (DefaultClientName when empty,
// cut to 80 characters) and returns the codes. info is shown to the
// approver as it is.
func (f *Flow) Start(clientName string, info map[string]string) (Start, error) {
	clientName = strings.TrimSpace(clientName)
	if clientName == "" {
		clientName = DefaultClientName
	}
	if len(clientName) > maxClientName {
		clientName = clientName[:maxClientName]
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.opts.Now()
	f.prune(now)
	if len(f.byDevice) >= MaxPending {
		return Start{}, ErrTooMany
	}
	deviceCode, err := randomDeviceCode()
	if err != nil {
		return Start{}, err
	}
	var userCode string
	for {
		if userCode, err = randomUserCode(); err != nil {
			return Start{}, err
		}
		if _, taken := f.byUser[userCode]; !taken {
			break
		}
	}
	req := &request{
		deviceCode: deviceCode, userCode: userCode, clientName: clientName,
		info: cloneInfo(info), created: now, expires: now.Add(f.opts.TTL),
	}
	f.byDevice[deviceCode] = req
	f.byUser[userCode] = req
	return Start{
		DeviceCode: deviceCode,
		UserCode:   Format(userCode),
		ExpiresIn:  int(f.opts.TTL / time.Second),
		Interval:   max(int(f.opts.Interval/time.Second), 1),
	}, nil
}

// Poll reports the outcome for a device code: ErrPending while it waits,
// ErrSlowDown when polled faster than half the interval, ErrDenied once
// (the request is then forgotten), ErrExpired once it expired, ErrUnknown
// for a code the flow does not hold, and the Link once approved; the link
// is handed over once and the request forgotten.
func (f *Flow) Poll(deviceCode string) (Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.opts.Now()
	req, ok := f.byDevice[deviceCode]
	if !ok {
		return Link{}, ErrUnknown
	}
	if !now.Before(req.expires) {
		f.remove(req)
		return Link{}, ErrExpired
	}
	if !req.lastPoll.IsZero() && now.Sub(req.lastPoll) < f.opts.Interval/2 {
		req.lastPoll = now
		return Link{}, ErrSlowDown
	}
	req.lastPoll = now
	switch req.state {
	case denied:
		f.remove(req)
		return Link{}, ErrDenied
	case approved:
		f.remove(req)
		return req.link, nil
	}
	return Link{}, ErrPending
}

// Approve mints a token for the request with that user code (formatting
// and case do not matter) under the approver's account. The token is named
// name, or the client's name, with -2, -3, … appended while the name is
// taken. projects is the token's project list; empty means the approver's
// own, and every named project must be covered by the approver's token.
func (f *Flow) Approve(userCode string, by auth.Principal, name string, projects []string) (Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, err := f.decidable(userCode)
	if err != nil {
		return Approval{}, err
	}
	granted, err := grantable(by, projects)
	if err != nil {
		return Approval{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = req.clientName
	}
	raw, used, err := f.mint(name, by.AccountID, granted)
	if err != nil {
		return Approval{}, err
	}
	req.state = approved
	req.link = Link{Token: raw, TokenID: used, Name: used, AccountID: by.AccountID}
	return Approval{UserCode: Format(req.userCode), Name: used, AccountID: by.AccountID, Projects: granted}, nil
}

// Deny refuses the request with that user code; its next poll reports
// ErrDenied.
func (f *Flow) Deny(userCode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, err := f.decidable(userCode)
	if err != nil {
		return err
	}
	req.state = denied
	return nil
}

// Pending lists the requests the flow holds, oldest first, approved and
// denied ones (not yet collected by their client) included.
func (f *Flow) Pending() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune(f.opts.Now())
	out := make([]Request, 0, len(f.byDevice))
	for _, r := range f.byDevice {
		out = append(out, Request{
			UserCode: Format(r.userCode), ClientName: r.clientName, ClientInfo: cloneInfo(r.info),
			Created: r.created, Expires: r.expires, State: r.state.String(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// RevokeToken revokes the named token in the token file, for a client
// logging out.
func (f *Flow) RevokeToken(name string) error {
	return f.opts.Tokens.Revoke(name)
}

// Normalize reduces a user code to its letters and digits, upper-cased, so
// "bcdf-ghjk" and "BCDF GHJK" name the same request.
func Normalize(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Format renders a normalized user code for people: BCDF-GHJK.
func Format(norm string) string {
	if len(norm) == userCodeLen {
		return norm[:userCodeLen/2] + "-" + norm[userCodeLen/2:]
	}
	return norm
}

// decidable finds a pending, unexpired request by user code; the lock is held.
func (f *Flow) decidable(userCode string) (*request, error) {
	req, ok := f.byUser[Normalize(userCode)]
	if !ok {
		return nil, ErrNoRequest
	}
	if !f.opts.Now().Before(req.expires) {
		f.remove(req)
		return nil, ErrExpired
	}
	if req.state != pending {
		return nil, ErrDecided
	}
	return req, nil
}

// grantable returns the project list a principal may grant: its own when
// none is named, else the named ones, each of which it must cover.
func grantable(by auth.Principal, projects []string) ([]string, error) {
	if len(projects) == 0 {
		out := slices.Clone(by.Projects)
		if out == nil {
			out = []string{}
		}
		return out, nil
	}
	out := []string{}
	for _, p := range projects {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			if !slices.Contains(by.Projects, "*") {
				return nil, fmt.Errorf("%w: %q", ErrProject, p)
			}
		} else if !auth.AllowProject(by, p) {
			return nil, fmt.Errorf("%w: %q", ErrProject, p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// mint creates the token, trying name, name-2, name-3, … while the name is
// taken.
func (f *Flow) mint(name, accountID string, projects []string) (raw, used string, err error) {
	used = name
	for i := 2; i <= maxNameTries; i++ {
		raw, err = f.opts.Tokens.Create(used, accountID, projects)
		if err == nil {
			return raw, used, nil
		}
		if !errors.Is(err, auth.ErrTokenExists) {
			return "", "", err
		}
		used = fmt.Sprintf("%s-%d", name, i)
	}
	return "", "", fmt.Errorf("%w: %q and its numbered variants", auth.ErrTokenExists, name)
}

func (f *Flow) remove(req *request) {
	delete(f.byDevice, req.deviceCode)
	delete(f.byUser, req.userCode)
}

func (f *Flow) prune(now time.Time) {
	for _, req := range f.byDevice {
		if !now.Before(req.expires) {
			f.remove(req)
		}
	}
}

func cloneInfo(info map[string]string) map[string]string {
	if len(info) == 0 {
		return nil
	}
	out := make(map[string]string, len(info))
	for k, v := range info {
		out[k] = v
	}
	return out
}

func randomDeviceCode() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("device: generate code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomUserCode() (string, error) {
	out := make([]byte, userCodeLen)
	n := big.NewInt(int64(len(userCodeAlphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", fmt.Errorf("device: generate code: %w", err)
		}
		out[i] = userCodeAlphabet[v.Int64()]
	}
	return string(out), nil
}
