package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGTrigger(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid trigger source shape")
	}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Name, sqltype.Bool, sqltype.Oid} {
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != in.Source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign trigger source type")
		}
	}
	rows := make([][]sqltype.Value, 0)
	appendTrigger := func(oid ids.OID, relation ids.ObjectAddress, name string, internal bool, constraint ids.OID) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, 5)
		for _, field := range [...]struct {
			slot int
			oid  ids.OID
		}{{0, oid}, {1, relation.ObjectID}, {4, constraint}} {
			var err error
			row[field.slot], err = sqltype.OIDValue(in.Source.Columns[field.slot].Usage.Type, uint32(field.oid))
			if err != nil {
				return err
			}
		}
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, name)
		row[3] = sqltype.Boolean(in.Source.Columns[3].Usage.Type, internal)
		rows = append(rows, row)
		return nil
	}
	for _, relation := range in.Catalog.Relations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := relation.VisitTriggers(func(trigger catalog.TriggerFact) error {
			return appendTrigger(trigger.OID, relation.Address(), trigger.Name, false, 0)
		}); err != nil {
			return nil, err
		}
	}
	for trigger := range catalog.ForeignKeyTriggerFacts(in.Catalog) {
		if err := appendTrigger(trigger.OID, trigger.Relation, trigger.Name, true, trigger.Constraint.OID); err != nil {
			return nil, err
		}
	}
	return rows, in.Context.Err()
}
