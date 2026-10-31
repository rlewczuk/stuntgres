package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// updateModdatetime replaces only the borrowed NEW slot, as the released helper
// replaces the physical attribute without applying the column's typmod again.
func updateModdatetime[A tsearch.Authority[A]](h Host[A], relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next []sqltype.Value) error {
	if p.Level != stored.TriggerRow { return sqlError("XX000", "moddatetime: must be fired for row") }
	if p.Timing != stored.TriggerBefore { return sqlError("XX000", "moddatetime: must be fired before event") }
	if event == stored.TriggerInsert { return sqlError("XX000", "moddatetime: cannot process INSERT events") }
	if event == stored.TriggerDelete { return sqlError("XX000", "moddatetime: cannot process DELETE events") }
	if len(p.Arguments) != 1 { return sqlError("XX000", fmt.Sprintf("moddatetime (%s): A single argument was expected", relation.Name())) }
	name := p.Arguments[0]
	slot, ok := relation.Index[name]
	if !ok { return sqlError("09000", fmt.Sprintf(`"%s" has no attribute "%s"`, relation.Name(), name)) }
	typ := relation.Columns[slot].Usage.Type
	if typ.ID() != sqltype.Timestamp && typ.ID() != sqltype.Timestamptz { return sqlError("09000", fmt.Sprintf(`attribute "%s" of "%s" must be type TIMESTAMP or TIMESTAMPTZ`, name, relation.Name())) }
	input, _ := h.Environments()
	value, err := sqltype.CurrentDateTime(typ.Usage(), input)
	if err != nil { return err }
	next[slot] = value
	return nil
}
