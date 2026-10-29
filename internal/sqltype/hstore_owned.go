package sqltype

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// HstoreEntry is an owned transport projection, never private SQL backing.
type HstoreEntry struct {
	Key, Value string
	Null       bool
}

func (v Value) OwnedHstoreEntries(ctx context.Context, descriptor *Descriptor) ([]HstoreEntry, error) {
	if descriptor == nil || !descriptor.IsHstore() || v.typ != descriptor || v.null || !v.validHstore() {
		return nil, fmt.Errorf("invalid hstore projection generation")
	}
	entries := make([]HstoreEntry, len(v.hstore.pairs))
	for i, p := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries[i] = HstoreEntry{p.key, p.value, p.null}
	}
	return entries, nil
}

// HstoreFromEntries validates an untrusted carrier and copies it into fresh
// immutable SQL backing. No application-owned slice survives construction.
func (d *Descriptor) HstoreFromEntries(ctx context.Context, entries []HstoreEntry) (Value, error) {
	if !d.IsHstore() {
		return Value{}, fmt.Errorf("invalid hstore construction generation")
	}
	pairs := make([]hstorePair, len(entries))
	for i, p := range entries {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if !utf8.ValidString(p.Key) || strings.IndexByte(p.Key, 0) >= 0 || !p.Null && (!utf8.ValidString(p.Value) || strings.IndexByte(p.Value, 0) >= 0) || p.Null && p.Value != "" {
			return Value{}, fmt.Errorf("invalid host hstore key/value")
		}
		pairs[i] = hstorePair{p.Key, p.Value, p.Null}
	}
	return d.ownedHstore(ctx, pairs)
}
