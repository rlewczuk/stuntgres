package pgcatalog

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// procedureFormalArrays projects declaration-owned formals, never expanded call
// operands or a guessed structural result. All-IN declarations use SQL NULL.
// Non-SQL inputs reuse the already materialized Signature vector.
func procedureFormalArrays(ctx context.Context, d *catalog.Procedure, types, modes *sqltype.Descriptor, variadic ids.OID, inputTypes sqltype.Value) (sqltype.Value, sqltype.Value, error) {
	allTypes, allModes := sqltype.Null(types), sqltype.Null(modes)
	count := 0
	if d.SQL != nil {
		mixed := false
		for _, formal := range d.SQL.Formals {
			mixed = mixed || formal.Mode != "i"
		}
		if !mixed {
			return allTypes, allModes, nil
		}
		count = len(d.SQL.Formals)
	} else {
		outputs := d.OutputDeclaration()
		if outputs == nil && variadic == 0 {
			return allTypes, allModes, nil
		}
		inputs := inputTypes.ArrayLen()
		if inputs != int(ProcedureArity(d)) {
			return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: invalid routine input vector")
		}
		count = inputs
		if outputs != nil {
			count += outputs.Len()
		}
	}
	typeValues := make([]sqltype.Value, count)
	modeValues := make([]sqltype.Value, count)
	if err := VisitProcedureFormals(ctx, d, inputTypes, func(f ProcedureFormal) error {
		if f.Index >= count {
			return fmt.Errorf("stuntgres: invalid formal array width")
		}
		value, err := sqltype.OIDValue(types.Element(), uint32(f.Type))
		if err != nil {
			return err
		}
		typeValues[f.Index] = value
		modeValues[f.Index] = sqltype.String(modes.Element(), f.Mode)
		return nil
	}); err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	dimensions := []sqltype.ArrayDimension{{Lower: 1, Length: int32(count)}}
	var err error
	allTypes, err = sqltype.OwnedArrayValue(types, dimensions, typeValues)
	if err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	allModes, err = sqltype.OwnedArrayValue(modes, dimensions, modeValues)
	return allTypes, allModes, err
}
