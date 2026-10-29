package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// updateModdatetime replaces only the borrowed NEW slot, as the released helper
// replaces the physical attribute without applying the column's typmod again.
func (s *StatementScope) updateModdatetime(relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next []sqltype.Value) error {
	if p.Level != stored.TriggerRow { return SQLError("XX000", "moddatetime: must be fired for row") }
	if p.Timing != stored.TriggerBefore { return SQLError("XX000", "moddatetime: must be fired before event") }
	if event == stored.TriggerInsert { return SQLError("XX000", "moddatetime: cannot process INSERT events") }
	if event == stored.TriggerDelete { return SQLError("XX000", "moddatetime: cannot process DELETE events") }
	if len(p.Arguments) != 1 { return SQLError("XX000", fmt.Sprintf("moddatetime (%s): A single argument was expected", relation.Name())) }
	name := p.Arguments[0]
	slot, ok := relation.Index[name]
	if !ok { return SQLError("09000", fmt.Sprintf(`"%s" has no attribute "%s"`, relation.Name(), name)) }
	typ := relation.Columns[slot].Usage.Type
	if typ.ID() != sqltype.Timestamp && typ.ID() != sqltype.Timestamptz { return SQLError("09000", fmt.Sprintf(`attribute "%s" of "%s" must be type TIMESTAMP or TIMESTAMPTZ`, name, relation.Name())) }
	value, err := sqltype.CurrentDateTime(typ.Usage(), s.Input)
	if err != nil { return err }
	next[slot] = value
	return nil
}
