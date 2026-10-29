package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// insertTriggerUsername borrows the producer's NEW row for this invocation only.
// Literal column arguments are resolved against the current admitted relation.
func (s *StatementScope) insertTriggerUsername(relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next []sqltype.Value) error {
	if p.Level != stored.TriggerRow {
		return SQLError("XX000", "insert_username: must be fired for row")
	}
	if p.Timing != stored.TriggerBefore {
		return SQLError("XX000", "insert_username: must be fired before event")
	}
	if event != stored.TriggerInsert && event != stored.TriggerUpdate {
		return SQLError("XX000", "insert_username: cannot process DELETE events")
	}
	if len(p.Arguments) != 1 {
		return SQLError("XX000", fmt.Sprintf("insert_username (%s): one argument was expected", relation.Name()))
	}
	name := p.Arguments[0]
	slot, ok := relation.Index[name]
	if !ok || slot < 0 || slot >= len(relation.Columns) || slot >= len(relation.Attributes) || relation.Attributes[slot] <= 0 {
		return SQLError("09000", `"`+relation.Name()+`" has no attribute "`+name+`"`)
	}
	text := relation.Columns[slot].Usage.Type
	if text.ID() != sqltype.Text {
		return SQLError("09000", `attribute "`+name+`" of "`+relation.Name()+`" must be type TEXT`)
	}
	identity, err := s.WriteHost().IdentityValue(&exec.Context{}, functions.IdentityCurrentUser, text)
	if err != nil { return err }
	username, err := identity.Text()
	if err != nil { return err }
	next[slot] = sqltype.String(text, username)
	return nil
}
