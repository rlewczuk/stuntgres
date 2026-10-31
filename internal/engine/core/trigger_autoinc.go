package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// autoincTrigger reads the tuple entering this invocation for every pair. Output
// is cloned only on the first replacement; duplicate targets still allocate for
// each original NULL/zero occurrence, with the final replacement winning.
func (s *StatementScope) autoincTrigger(relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, original []sqltype.Value) ([]sqltype.Value, error) {
	if p.Level != stored.TriggerRow { return original, SQLError("XX000", "must be fired for row") }
	if p.Timing != stored.TriggerBefore { return original, SQLError("XX000", "must be fired before event") }
	if event == stored.TriggerDelete { return original, SQLError("XX000", "cannot process DELETE events") }
	if len(p.Arguments) == 0 || len(p.Arguments)%2 != 0 {
		return original, SQLError("XX000", fmt.Sprintf("autoinc (%s): even number gt 0 of arguments was expected", relation.Name()))
	}
	result := original
	changed := false
	for i := 0; i < len(p.Arguments); i += 2 {
		if err := s.Op.Context().Err(); err != nil { return original, err }
		name := p.Arguments[i]
		slot, ok := relation.Index[name]
		if !ok || slot < 0 || slot >= len(relation.Attributes) || relation.Attributes[slot] <= 0 {
			return original, SQLError("09000", fmt.Sprintf(`"%s" has no attribute "%s"`, relation.Name(), name))
		}
		typ := relation.Columns[slot].Usage.Type
		if typ == nil || typ.ID() != sqltype.Int4 {
			return original, SQLError("09000", fmt.Sprintf(`attribute "%s" of "%s" must be type INT4`, name, relation.Name()))
		}
		if slot >= len(original) { return original, fmt.Errorf("stuntgres: incomplete autoinc input tuple") }
		value := original[slot]
		if !value.IsNull() {
			n, err := value.Integer()
			if err != nil { return original, err }
			if n != 0 { continue }
		}
		n, err := s.WriteHost().NextvalText(p.Arguments[i+1])
		if err != nil { return original, err }
		narrowed := int32(n)
		if narrowed == 0 {
			n, err = s.WriteHost().NextvalText(p.Arguments[i+1])
			if err != nil { return original, err }
			narrowed = int32(n)
		}
		value, err = sqltype.Integer(typ, int64(narrowed))
		if err != nil { return original, err }
		if !changed { result = slices.Clone(original); changed = true }
		result[slot] = value
	}
	return result, nil
}
