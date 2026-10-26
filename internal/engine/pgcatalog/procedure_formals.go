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
	count, inputs := 0, 0
	outputs := d.OutputDeclaration()
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
		if outputs == nil && variadic == 0 {
			return allTypes, allModes, nil
		}
		inputs = inputTypes.ArrayLen()
		count = inputs
		if outputs != nil {
			if outputs.Inputs() != inputs || variadic != 0 {
				return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: invalid fixed output declaration")
			}
			count += outputs.Len()
		} else if inputs == 0 {
			return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: missing variadic input declaration")
		}
	}
	typeValues := make([]sqltype.Value, count)
	modeValues := make([]sqltype.Value, count)
	for i := range count {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
		var id uint32
		mode := "i"
		if d.SQL != nil {
			formal := d.SQL.Formals[i]
			id, mode = uint32(formal.Usage.Type.ID()), formal.Mode
		} else if i < inputs {
			n, err := inputTypes.ArrayElement(i).OID()
			if err != nil {
				return sqltype.Value{}, sqltype.Value{}, err
			}
			id = n
			if variadic != 0 && i == inputs-1 {
				mode = "v"
			}
		} else {
			id, mode = uint32(outputs.Type(i-inputs)), "o"
		}
		value, err := sqltype.OIDValue(types.Element(), id)
		if err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
		typeValues[i] = value
		modeValues[i] = sqltype.String(modes.Element(), mode)
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
