package sqltype

import (
	"context"
	"fmt"
)

func snapshotDatumLoss() error {
	return fmt.Errorf("stuntgres: materialized view snapshot coercion changes persisted datum")
}

// snapshotOIDIdentity identifies the shared unsigned carrier, not signed SQL
// integers whose OID casts can wrap or change the numeric value.
func snapshotOIDIdentity(id ID) bool {
	return id == Oid || id == Regclass || id == Regtype || id == Regrole || IsOperatorAlias(id) || IsRoutineAlias(id)
}

// applyExactSnapshot uses the selected codec twice only where sizing can change
// a datum. The source is already evaluated once by the caller. Recursive array
// and domain operations retain the policy, including input hidden in IO casts.
func (p CoercionPlan) applyExactSnapshot(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if v.typ != p.source.Type || !p.target.Type.Concrete() {
		return Value{}, fmt.Errorf("foreign coercion value")
	}
	if v.null {
		return Null(p.target.Type), nil
	}
	// These are the persisted literal codecs and physical retags, not arbitrary
	// semantic SQL conversions (numeric-to-integer, timestamp-to-date, etc.).
	switch p.method {
	case identity, integerConversion, stringConversion:
	case oidConversion:
		if !snapshotOIDIdentity(p.source.Type.id) || !snapshotOIDIdentity(p.target.Type.id) {
			return Value{}, snapshotDatumLoss()
		}
	case numericConversion:
		if p.target.Type.id != Numeric || (p.source.Type.id != Int2 && p.source.Type.id != Int4 && p.source.Type.id != Int8 && p.source.Type.id != Numeric) {
			return Value{}, snapshotDatumLoss()
		}
	case viaIO:
		// The serializer resolves a qualified routine signature, retags its
		// unsigned identity to OID, then emits decimal text into the container
		// codec. This preserves identity independently of symbolic spelling.
		if !IsCharacter(p.source.Type.id) && !(p.source.Type.id == Oid && IsCharacter(p.target.Type.id)) {
			return Value{}, snapshotDatumLoss()
		}
	default:
		return Value{}, snapshotDatumLoss()
	}
	unsized := p
	unsized.target.Typmod = -1
	baseline, err := unsized.applyBaseUnchecked(ctx, v, input, output)
	if err != nil {
		return Value{}, err
	}
	// Character retags must preserve bytes even before the modifier is applied:
	// BPCHAR-to-TEXT normally removes padding in ordinary SQL.
	if p.method == stringConversion && baseline.text != v.text {
		return Value{}, snapshotDatumLoss()
	}
	if p.target.Typmod < 0 {
		return baseline, nil
	}
	value, err := p.applyBaseUnchecked(ctx, v, input, output)
	if err != nil {
		return Value{}, err
	}
	if !SameDatum(baseline, value) {
		return Value{}, snapshotDatumLoss()
	}
	return value, nil
}
