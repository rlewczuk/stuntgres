package table

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DropIdentity is old with DROP IDENTITY of action: the identity sequence is
// captured at its grant, then removed with its view dependents. It is the
// provider algorithm shared by ordinary execution and immutable preparation;
// only the execution sink owns a stage.
func DropIdentity(provider DropDependencyReader, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	if catalog.IsSystemColumn(action.Column.Text) {
		return old, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	slot, present := old.Index[action.Column.Text]
	if !present {
		return old, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.Name()))
	}
	attribute := old.Attributes[slot]
	identity, exists := old.ColumnMeta.Identities[attribute]
	if !exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" is not an identity column`, action.Column.Text, old.Name())
		if !action.IfExists {
			return old, sqlError("55000", message)
		}
		provider.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message + ", skipping"})
		return old, nil
	}
	ref := provider.BaseRef(identity.Sequence)
	if err := provider.CaptureIdentitySequence(old, attribute, ref); err != nil {
		return old, err
	}
	if err := provider.ApplyViewDependents([]ids.ObjectAddress{identity.Sequence}, parser.DropRestrict); err != nil {
		return old, err
	}
	if err := provider.Remove(ref); err != nil {
		return old, err
	}
	provider.TombstoneIdentity(identity.Sequence)
	next := *old
	next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
	delete(next.ColumnMeta.Identities, attribute)
	return &next, nil
}
