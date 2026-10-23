package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// PGSequencesInput holds what the pg_sequences rows read. Err is the
// statement's cancellation check, called before each definition. Visit walks
// the sequence definitions (the retained base with the owner's overlay
// applied), Namespace returns a definition's namespace through the same
// overlay, and Temporary is the session's own TEMP owner.
type PGSequencesInput struct {
	Source    *catalog.Relation
	Err       func() error
	Visit     func(func(catalog.Descriptor) error) error
	Namespace func(ids.ObjectAddress) catalog.Descriptor
	Temporary ids.SessionToken
	Roles     authorization.Read
}

// CapturePGSequences builds the pg_sequences rows, leaving last_value NULL for
// the engine's lazy completion, and returns each row's sequence address in row
// order. Caller holds Engine.mu; no generator is accessed.
func CapturePGSequences(in PGSequencesInput) ([][]sqltype.Value, []ids.ObjectAddress, error) {
	source := in.Source
	rows := make([][]sqltype.Value, 0)
	var addresses []ids.ObjectAddress
	appendDefinition := func(descriptor catalog.Descriptor) error {
		if err := in.Err(); err != nil {
			return err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.SequenceRelation {
			return nil
		}
		ns, ok := in.Namespace(relation.Namespace()).(*catalog.Namespace)
		if !ok {
			return fmt.Errorf("stuntgres: sequence namespace missing")
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != in.Temporary {
			return nil
		}
		owner, err := in.Roles.Descriptor(relation.Owner)
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, 11)
		row[0] = sqltype.String(source.Columns[0].Usage.Type, ns.Name())
		row[1] = sqltype.String(source.Columns[1].Usage.Type, relation.Name())
		row[2] = sqltype.String(source.Columns[2].Usage.Type, owner.Name)
		row[3], err = sqltype.RegtypeValue(source.Columns[3].Usage.Type, uint32(relation.Sequence.SQLType))
		if err != nil {
			return err
		}
		for i, value := range [4]int64{relation.Sequence.Start, relation.Sequence.Min, relation.Sequence.Max, relation.Sequence.Increment} {
			row[i+4], err = sqltype.Integer(source.Columns[i+4].Usage.Type, value)
			if err != nil {
				return err
			}
		}
		row[8] = sqltype.Boolean(source.Columns[8].Usage.Type, relation.Sequence.Cycle)
		row[9], err = sqltype.Integer(source.Columns[9].Usage.Type, relation.Sequence.Cache)
		if err != nil {
			return err
		}
		row[10] = sqltype.Null(source.Columns[10].Usage.Type)
		rows = append(rows, row)
		addresses = append(addresses, relation.Address())
		return nil
	}
	if err := in.Visit(appendDefinition); err != nil {
		return nil, nil, err
	}
	return rows, addresses, nil
}
