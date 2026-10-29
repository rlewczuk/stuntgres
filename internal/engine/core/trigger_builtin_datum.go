package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// triggerRowChanged compares physical user-value slots, not SQL equality. Rows
// and datums stay borrowed and cancellation never produces a suppression result.
func triggerRowChanged(ctx context.Context, old, next []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(old) != len(next) {
		return false, fmt.Errorf("stuntgres: invalid trigger row shape")
	}
	for i, value := range old {
		same, err := value.SamePayload(ctx, next[i])
		if err != nil {
			return false, err
		}
		if !same {
			return true, nil
		}
	}
	return false, ctx.Err()
}

// NEW at the BEFORE boundary is a transient, not-yet-stored tuple. Its tableoid
// is InvalidOid, including on UPDATE; the producer relation identity is not a
// value carried by that tuple. Other reserved system attributes currently fail
// the builtin's type checks and have no executable datum carrier here.
func triggerNewColumnValue(read catalog.Read, next []sqltype.Value, slot int) (sqltype.Value, error) {
	if slot >= 0 && slot < len(next) {
		return next[slot], nil
	}
	if slot == -6 {
		typ, ok := read.LookupType(sqltype.Oid)
		if ok {
			return sqltype.OIDValue(typ, 0)
		}
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unsupported trigger NEW attribute")
}

// The caller has established implicit binary coercibility against this read.
// Reuse its physical carrier mapping rather than rendering/reparsing a datum.
func triggerBinaryValue(ctx context.Context, read catalog.Read, value sqltype.Value, target sqltype.ID) (sqltype.Value, error) {
	typ, ok := read.LookupType(target)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing trigger binary target")
	}
	if value.Type() == typ {
		return value, ctx.Err()
	}
	plan, err := sqltype.PlanDeclaredBinaryCoercion(value.Type().Usage(), typ.Usage())
	if err != nil {
		return sqltype.Value{}, err
	}
	return plan.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
}
