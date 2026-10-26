package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoTriggersOID is a native source identity, not a PostgreSQL allocation.
const InfoTriggersOID ids.OID = 16237

var infoTriggersColumns = []ColumnSpec{
	{"trigger_catalog", 13248, true, 950, 0, -1},
	{"trigger_schema", 13248, true, 950, 0, -1},
	{"trigger_name", 13248, true, 950, 0, -1},
	{"event_manipulation", 13246, true, 950, 0, -1},
	{"event_object_catalog", 13248, true, 950, 0, -1},
	{"event_object_schema", 13248, true, 950, 0, -1},
	{"event_object_table", 13248, true, 950, 0, -1},
	{"action_order", 13243, true, 0, 0, -1},
	{"action_condition", 13246, true, 950, 0, -1},
	{"action_statement", 13246, true, 950, 0, -1},
	{"action_orientation", 13246, true, 950, 0, -1},
	{"action_timing", 13246, true, 950, 0, -1},
	{"action_reference_old_table", 13248, true, 950, 0, -1},
	{"action_reference_new_table", 13248, true, 950, 0, -1},
	{"action_reference_old_row", 13248, true, 950, 0, -1},
	{"action_reference_new_row", 13248, true, 950, 0, -1},
	{"created", 13254, true, 0, 0, -1},
}

func captureInfoTriggers(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoTriggersOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != len(infoTriggersColumns) {
		return nil, fmt.Errorf("stuntgres: invalid triggers source")
	}
	for i, column := range in.Source.Columns {
		typ := column.Usage.Type
		expected, ok := in.Catalog.LookupType(infoTriggersColumns[i].Type)
		if !ok || typ != expected || !typ.IsDomain() {
			return nil, fmt.Errorf("stuntgres: invalid triggers domain")
		}
		base := sqltype.Name
		switch i {
		case 3, 8, 9, 10, 11:
			base = sqltype.VarChar
		case 7:
			base = sqltype.Int4
		case 16:
			base = sqltype.Timestamptz
		}
		if typ.BaseType().ID() != base {
			return nil, fmt.Errorf("stuntgres: invalid triggers domain base")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || !relation.HasTriggers() {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing triggers namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		owner, visible, err := infoTriggerRelationVisible(in, relation)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		var ranks [3]int64
		err = in.Runtime.VisitTriggerDefinitions(in.Context, in.Catalog, relation, owner, func(f TriggerFact) error {
			for event, enabled := range [3]bool{f.Insert, f.Delete, f.Update} {
				if err := in.Context.Err(); err != nil {
					return err
				}
				if !enabled {
					continue
				}
				ranks[event]++
				row := make([]sqltype.Value, len(infoTriggersColumns))
				for i, c := range in.Source.Columns {
					row[i] = sqltype.Null(c.Usage.Type)
				}
				texts := [14]string{in.Runtime.DatabaseName(), namespace.Name(), f.Name, [3]string{"INSERT", "DELETE", "UPDATE"}[event], in.Runtime.DatabaseName(), namespace.Name(), relation.Name(), "", f.Condition, f.Statement, f.Orientation, f.Timing, f.OldTable, f.NewTable}
				for i, text := range texts {
					if i == 7 || i == 8 && !f.HasCondition || i == 12 && !f.HasOldTable || i == 13 && !f.HasNewTable {
						continue
					}
					typ := in.Source.Columns[i].Usage.Type
					value, err := typ.Wrap(sqltype.String(typ.BaseType(), text))
					if err != nil {
						return err
					}
					row[i] = value
				}
				typ := in.Source.Columns[7].Usage.Type
				value, err := sqltype.Integer(typ.BaseType(), ranks[event])
				if err != nil {
					return err
				}
				row[7], err = typ.Wrap(value)
				if err != nil {
					return err
				}
				rows = append(rows, row)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// SELECT and MAINTAIN do not expose triggers. WHEN disclosure is owner-only.
func infoTriggerRelationVisible(in Input, relation *catalog.Relation) (owner, visible bool, err error) {
	owner, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), relation.Owner)
	if err != nil || owner {
		return owner, owner, err
	}
	ref := in.Ref.Domain().Ref(in.Ref.Epoch, relation.Address())
	rights, err := in.Runtime.Effective(ref, false)
	if err != nil {
		return false, false, err
	}
	if rights&(authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege|authorization.TruncatePrivilege|authorization.ReferencesPrivilege|authorization.TriggerPrivilege) != 0 {
		return false, true, nil
	}
	for _, attribute := range relation.Attributes {
		if err := in.Context.Err(); err != nil {
			return false, false, err
		}
		if attribute <= 0 {
			continue
		}
		if _, dropped := relation.ColumnMeta.Dropped[attribute]; dropped {
			continue
		}
		column := ref
		column.Address.SubID = attribute
		rights, err := in.Runtime.Effective(column, false)
		if err != nil {
			return false, false, err
		}
		if rights&(authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) != 0 {
			return false, true, nil
		}
	}
	return false, false, nil
}
