package codeparse

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Key identifies one parse: the SHA-256 of the file's (redacted) content as
// the code store records it, and the facts version it was parsed with
// (Profile.FactsVersion or TextFactsVersion). A file is parsed once per key,
// for every branch, layer and member holding that content; a version bump
// makes old entries unreachable, so they are re-parsed lazily.
type Key struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// ErrInvalidKey is returned for a key whose SHA256 is not 64 lowercase hex
// characters or whose Version is empty.
var ErrInvalidKey = errors.New("codeparse: invalid cache key")

// Validate checks k.
func (k Key) Validate() error {
	if len(k.SHA256) != sha256.Size*2 || k.Version == "" {
		return fmt.Errorf("%w: %q@%q", ErrInvalidKey, k.SHA256, k.Version)
	}
	for _, c := range k.SHA256 {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: %q@%q", ErrInvalidKey, k.SHA256, k.Version)
		}
	}
	return nil
}

// ContentKey returns the key of content parsed with version.
func ContentKey(content []byte, version string) Key {
	sum := sha256.Sum256(content)
	return Key{SHA256: hex.EncodeToString(sum[:]), Version: version}
}

// Cache stores facts by Key. Implementations (the memory cache here, the
// Mongo code_parses collection) are safe for concurrent use, store a copy of
// what they are given and return a copy, so callers may change either.
type Cache interface {
	// Get returns the facts stored under key; ok is false on a miss.
	Get(ctx context.Context, key Key) (facts Facts, ok bool, err error)
	// Put stores facts under key, replacing what was there.
	Put(ctx context.Context, key Key, facts Facts) error
}

// Cached returns the facts for key from c, calling parse on a miss and
// storing its result. A parse error is returned and nothing is stored; a
// failed Put does not lose the parse (its facts are returned with the error
// joined, so the caller may index them and log the cache failure).
func Cached(ctx context.Context, c Cache, key Key, parse func() (Facts, error)) (Facts, error) {
	if err := key.Validate(); err != nil {
		return Facts{}, err
	}
	facts, ok, err := c.Get(ctx, key)
	if err != nil {
		return Facts{}, err
	}
	if ok {
		return facts, nil
	}
	facts, err = parse()
	if err != nil {
		return Facts{}, err
	}
	if err := c.Put(ctx, key, facts); err != nil {
		return facts, fmt.Errorf("codeparse: cache put: %w", err)
	}
	return facts, nil
}

// MemoryCache is a Cache in memory, evicting the least recently used entry
// beyond its capacity.
type MemoryCache struct {
	mu      sync.Mutex
	max     int
	order   *list.List // front is most recently used; values are *memEntry
	entries map[Key]*list.Element
}

type memEntry struct {
	key   Key
	facts Facts
}

// NewMemoryCache returns a memory cache holding at most maxEntries facts;
// zero or less means no limit.
func NewMemoryCache(maxEntries int) *MemoryCache {
	return &MemoryCache{max: maxEntries, order: list.New(), entries: map[Key]*list.Element{}}
}

// Get implements Cache.
func (m *MemoryCache) Get(ctx context.Context, key Key) (Facts, bool, error) {
	if err := ctx.Err(); err != nil {
		return Facts{}, false, err
	}
	if err := key.Validate(); err != nil {
		return Facts{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.entries[key]
	if !ok {
		return Facts{}, false, nil
	}
	m.order.MoveToFront(el)
	return el.Value.(*memEntry).facts.Clone(), true, nil
}

// Put implements Cache.
func (m *MemoryCache) Put(ctx context.Context, key Key, facts Facts) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	facts = facts.Clone()
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.entries[key]; ok {
		el.Value.(*memEntry).facts = facts
		m.order.MoveToFront(el)
		return nil
	}
	m.entries[key] = m.order.PushFront(&memEntry{key: key, facts: facts})
	for m.max > 0 && m.order.Len() > m.max {
		oldest := m.order.Back()
		m.order.Remove(oldest)
		delete(m.entries, oldest.Value.(*memEntry).key)
	}
	return nil
}

// Len returns the number of entries held.
func (m *MemoryCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order.Len()
}
