package core

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// manageLargeObjectReference borrows the row chain's current NEW and original
// OLD. Unlink participates in the ordinary statement catalog journal; no row
// copies, reference counting or separate rollback authority are introduced.
func (s *StatementScope) manageLargeObjectReference(relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value, targets []int32) error {
	if p.Level != stored.TriggerRow {
		return SQLError("XX000", p.Name+": must be fired for row")
	}
	if len(p.Arguments) == 0 {
		return SQLError("XX000", p.Name+": no column name provided in the trigger definition")
	}
	name := p.Arguments[0]
	slot, ok := relation.Index[name]
	if !ok || slot < 0 || relation.Attributes[slot] <= 0 {
		return SQLError("XX000", fmt.Sprintf(`%s: column "%s" does not exist`, p.Name, name))
	}
	compare := event == stored.TriggerUpdate
	if event != stored.TriggerDelete && (!compare || !slices.Contains(targets, relation.Attributes[slot])) {
		return nil
	}
	var fresh sqltype.Value
	if compare {
		fresh = next[slot]
	}
	oid, unlink, err := loManageReference(s.executionContext(), old[slot], fresh, compare, s.Output)
	if err != nil || !unlink {
		return err
	}
	// The released helper calls be_lo_unlink directly. This deliberately does
	// not perform a second SQL function EXECUTE check on lo_unlink.
	_, err = (*statementHost)(s).LargeObject(functions.LargeObjectRequest{Name: "lo_unlink", OID: ids.OID(oid)})
	return err
}

func loManageReference(ctx context.Context, old, fresh sqltype.Value, compare bool, output sqltype.OutputEnvironment) (uint32, bool, error) {
	if old.IsNull() {
		return 0, false, nil
	}
	base, err := old.BaseValue()
	if err != nil {
		return 0, false, err
	}
	if base.Type().ID() == sqltype.Oid {
		oid, err := base.OID()
		if err != nil {
			return 0, false, err
		}
		if compare && !fresh.IsNull() {
			current, err := fresh.BaseValue()
			if err != nil {
				return 0, false, err
			}
			newOID, err := current.OID()
			if err != nil {
				return 0, false, err
			}
			if oid == newOID {
				return 0, false, nil
			}
		}
		return oid, true, nil
	}
	// SPI_getvalue compares actual output strings, not typed equality. Keep
	// the common OID/domain path above inline and use the shared output hooks
	// for other admitted representations, including text and integer columns.
	original, err := old.Type().OutputWithEnvironment(ctx, old, output)
	if err != nil {
		return 0, false, err
	}
	if compare && !fresh.IsNull() {
		current, err := fresh.Type().OutputWithEnvironment(ctx, fresh, output)
		if err != nil {
			return 0, false, err
		}
		if original == current {
			return 0, false, nil
		}
	}
	return loManageOutputOID(original), true, nil
}

// lo_manage uses atooid, not the OID SQL input function: decimal strtoul
// accepts whitespace/signs and a numeric prefix, ignores trailing text and
// narrows to Oid. Overflow of the released 64-bit oracle's unsigned long
// saturates before narrowing; it must not become an input-syntax error.
func loManageOutputOID(text string) uint32 {
	i := 0
	for i < len(text) {
		switch text[i] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			i++
			continue
		}
		break
	}
	negative := false
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		negative = text[i] == '-'
		i++
	}
	var value uint64
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		digit := uint64(text[i] - '0')
		if value > (^uint64(0)-digit)/10 {
			return ^uint32(0)
		}
		value = value*10 + digit
		i++
	}
	if negative {
		value = -value
	}
	return uint32(value)
}
