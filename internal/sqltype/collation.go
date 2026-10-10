package sqltype

import (
	"fmt"
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
