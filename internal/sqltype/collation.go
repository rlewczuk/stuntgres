package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqltype/unicode16"
)

// RegisteredCollation keeps object identity separate from an admitted immutable
// execution provider. Callers obtain the provider from a validated catalog
// descriptor, not from SQL's arbitrary object OID.
func RegisteredCollation(id uint32, provider uint32) (CollationRef, error) {
	if id == 0 || provider != 950 && provider != 811 && provider != 6411 {
		return CollationRef{}, fmt.Errorf("invalid collation execution provider")
	}
	switch id {
	case 100, 950, 951:
		if provider != 950 {
			return CollationRef{}, fmt.Errorf("invalid bootstrap collation provider")
		}
	case 811, 6411:
		if provider != id {
			return CollationRef{}, fmt.Errorf("invalid bootstrap collation provider")
		}
	case 962, 963:
		return CollationRef{}, fmt.Errorf("inquiry-only bootstrap collation")
	}
	r := CollationRef{ID: id, Derivation: Default}
	if id != 100 && id != 950 && id != 951 && id != 811 && id != 6411 {
		r.provider = provider
		r.registeredID = id
	}
	return r, nil
}

func (r CollationRef) Valid() bool {
	if r.Derivation != Default {
		return false
	}
	switch r.ID {
	case 100, 950, 951, 811, 6411:
		return r.registeredID == 0 && r.provider == 0
	}
	return r.ID != 0 && r.registeredID == r.ID && (r.provider == 950 || r.provider == 811 || r.provider == 6411)
}

func (r CollationRef) CaseMode() unicode16.CaseMode {
	provider := r.provider
	if provider == 0 {
		provider = r.ID
	}
	switch provider {
	case 811:
		return unicode16.Simple
	case 6411:
		return unicode16.Full
	default:
		return unicode16.ASCII
	}
}

func (r CollationRef) Unicode() bool {
	return r.CaseMode() != unicode16.ASCII
}

// CompareText executes only an admitted registered deterministic provider.
// The providers have bytewise ordering; Unicode case mapping is unrelated.
func (r CollationRef) CompareText(ctx context.Context, a, b string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !r.Valid() {
		return 0, fmt.Errorf("invalid comparison collation")
	}
	n := min(len(a), len(b))
	for offset := 0; offset < n; offset += 1024 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(offset+1024, n)
		if sign := strings.Compare(a[offset:end], b[offset:end]); sign != 0 {
			return sign, nil
		}
	}
	if len(a) < len(b) {
		return -1, ctx.Err()
	}
	if len(a) > len(b) {
		return 1, ctx.Err()
	}
	return 0, ctx.Err()
}

func (r CollationRef) EqualText(ctx context.Context, a, b string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !r.Valid() {
		return false, fmt.Errorf("invalid equality collation")
	}
	return groupingTextEqual(ctx, a, b)
}

func (r CollationRef) HashText(ctx context.Context, seed uint64, text string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !r.Valid() {
		return 0, fmt.Errorf("invalid hashing collation")
	}
	return groupingTextHash(ctx, seed, text)
}
