package pgcatalog

import (
	"fmt"
	"math"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// capturePGTrigger reads actual immutable definitions without applying the
// distinct information_schema owner, privilege, WHEN or TEMP filters.
func capturePGTrigger(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil { return nil, err }
	view, source, ref := in.Catalog, in.Source, in.Ref
	address := ids.ObjectAddress{ClassID:ids.RelationClassID, ObjectID:ids.TriggerClassID}
	if view.Owner() == nil || view.State() == nil || in.Owner != view.Owner() || source == nil || source.Address() != address || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address != address || view.Get(address) != source || len(source.Columns) != 19 || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: foreign trigger source authority")
	}
	names := [...]string{"oid", "tgrelid", "tgparentid", "tgname", "tgfoid", "tgtype", "tgenabled", "tgisinternal", "tgconstrrelid", "tgconstrindid", "tgconstraint", "tgdeferrable", "tginitdeferred", "tgnargs", "tgattr", "tgargs", "tgqual", "tgoldtable", "tgnewtable"}
	for i, id := range [...]sqltype.ID{sqltype.Oid,sqltype.Oid,sqltype.Oid,sqltype.Name,sqltype.Oid,sqltype.Int2,sqltype.Char,sqltype.Bool,sqltype.Oid,sqltype.Oid,sqltype.Oid,sqltype.Bool,sqltype.Bool,sqltype.Int2,sqltype.Int2Vector,sqltype.Bytea,sqltype.PgNodeTree,sqltype.Name,sqltype.Name} {
		typ, ok := view.LookupType(id)
		if !ok || typ != source.Columns[i].Usage.Type || source.Columns[i].Name != names[i] || source.Columns[i].Nullable != (i >= 16) { return nil, fmt.Errorf("stuntgres: invalid trigger source column") }
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, ids.TriggerClassID); err != nil { return nil, err }
	rows := make([][]sqltype.Value, 0)
	seen := make(map[ids.OID]bool)
	for _, relation := range view.Relations() {
		if err := in.Context.Err(); err != nil { return nil, err }
		err := in.Runtime.VisitTriggerCatalog(in.Context, view, relation, source.Columns[16].Usage.Type, func(f TriggerCatalogFact) error {
			if err := in.Context.Err(); err != nil { return err }
			if f.OID == 0 || seen[f.OID] || f.Relation != relation.Address().ObjectID || f.Function == 0 || f.Name == "" || len(f.Arguments) > math.MaxInt16 || f.Qual.Type() != source.Columns[16].Usage.Type { return fmt.Errorf("stuntgres: malformed trigger definition") }
			seen[f.OID] = true
			row := make([]sqltype.Value, 19)
			for _, cell := range [...]struct{ index int; oid ids.OID }{{0,f.OID},{1,f.Relation},{2,f.Parent},{4,f.Function},{8,0},{9,0},{10,0}} {
				var err error
				row[cell.index], err = sqltype.OIDValue(source.Columns[cell.index].Usage.Type, uint32(cell.oid))
				if err != nil { return err }
			}
			row[3] = sqltype.String(source.Columns[3].Usage.Type, f.Name)
			var err error
			row[5], err = sqltype.Integer(source.Columns[5].Usage.Type, int64(f.Type))
			if err != nil { return err }
			row[6] = sqltype.String(source.Columns[6].Usage.Type, "O")
			for _, i := range [...]int{7,11,12} { row[i] = sqltype.Boolean(source.Columns[i].Usage.Type, false) }
			row[13], err = sqltype.Integer(source.Columns[13].Usage.Type, int64(len(f.Arguments)))
			if err != nil { return err }
			attrs := make([]int16, len(f.Attributes))
			for i, n := range f.Attributes {
				if n <= 0 || n > math.MaxInt16 { return fmt.Errorf("stuntgres: invalid trigger attribute number") }
				attrs[i] = int16(n)
			}
			row[14], err = sqltype.CatalogInt2Vector(source.Columns[14].Usage.Type, attrs)
			if err != nil { return err }
			size := 0
			for _, arg := range f.Arguments {
				if len(arg) >= math.MaxInt-size { return fmt.Errorf("stuntgres: trigger arguments too large") }
				size += len(arg)+1
			}
			var args strings.Builder
			args.Grow(size)
			for _, arg := range f.Arguments { args.WriteString(arg); args.WriteByte(0) }
			row[15], err = sqltype.Binary(source.Columns[15].Usage.Type, args.String())
			if err != nil { return err }
			row[16] = f.Qual
			row[17], row[18] = sqltype.Null(source.Columns[17].Usage.Type), sqltype.Null(source.Columns[18].Usage.Type)
			rows = append(rows, row)
			return nil
		})
		if err != nil { return nil, err }
	}
	for trigger := range catalog.ForeignKeyTriggerFacts(view) {
		if err := in.Context.Err(); err != nil { return nil, err }
		if trigger.OID == 0 || seen[trigger.OID] { return nil, fmt.Errorf("stuntgres: duplicate or zero trigger identity") }
		seen[trigger.OID] = true
		row, err := foreignKeyTriggerRow(in, trigger)
		if err != nil { return nil, err }
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil { return nil, err }
	return rows, nil
}

// These are persistent identities owned by admitted FK declarations, not rows
// inferred from absent implementation machinery or executable user payloads.
func foreignKeyTriggerRow(in Input, fact catalog.ForeignKeyTriggerFact) ([]sqltype.Value, error) {
	constraint := fact.Constraint
	for _, oid := range constraint.TriggerOIDs {
		if oid == 0 { return nil, fmt.Errorf("stuntgres: incomplete foreign-key trigger identity") }
	}
	owner, ownerOK := in.Catalog.Relation(fact.Owner)
	parent, parentOK := in.Catalog.Relation(constraint.Target)
	index, indexOK := in.Catalog.Relation(constraint.Index)
	if !ownerOK || !parentOK || !indexOK || owner.RelationKind != catalog.TableRelation || parent.RelationKind != catalog.TableRelation || constraint.OID == 0 || len(constraint.Attributes) == 0 || len(constraint.Attributes) != len(constraint.Referenced) || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Unique || !index.SQLIndex.Valid || !index.SQLIndex.Ready || !index.SQLIndex.Live || index.SQLIndex.Predicate != nil || len(index.SQLIndex.Keys) != len(constraint.Referenced) {
		return nil, fmt.Errorf("stuntgres: invalid foreign-key trigger provenance")
	}
	for i, attribute := range constraint.Attributes {
		matched := false
		for _, key := range index.SQLIndex.Keys {
			number, simple := in.Runtime.IndexSimpleAttribute(parent.Address(), key)
			if simple && number == constraint.Referenced[i] { matched = true; break }
		}
		if !matched { return nil, fmt.Errorf("stuntgres: foreign-key trigger index authority mismatch") }
		if !owner.HasAttribute(attribute) || !parent.HasAttribute(constraint.Referenced[i]) { return nil, fmt.Errorf("stuntgres: missing foreign-key trigger attribute") }
	}
	if fact.Slot < 0 || fact.Slot >= len(constraint.TriggerOIDs) || fact.OID != constraint.TriggerOIDs[fact.Slot] || fact.Relation != fact.Owner && fact.Relation != constraint.Target {
		return nil, fmt.Errorf("stuntgres: invalid foreign-key trigger identity")
	}
	var function ids.OID
	flags := int64(17)
	opposite := constraint.Target.ObjectID
	switch fact.Slot {
	case 0, 1:
		opposite = fact.Owner.ObjectID
		action := constraint.UpdateAction
		if fact.Slot == 0 { flags, action = 9, constraint.DeleteAction }
		switch action {
		case parser.ForeignKeyNoAction: function = 1654
		case parser.ForeignKeyRestrict: function = 1648
		case parser.ForeignKeyCascade: function = 1646
		case parser.ForeignKeySetNull: function = 1650
		case parser.ForeignKeySetDefault: function = 1652
		default: return nil, fmt.Errorf("stuntgres: invalid foreign-key trigger action")
		}
		if fact.Slot == 1 { function++ }
	case 2: flags, function = 5, 1644
	case 3: function = 1645
	default: return nil, fmt.Errorf("stuntgres: invalid foreign-key trigger slot")
	}
	row := make([]sqltype.Value, 19)
	for i := range row { row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type) }
	for _, cell := range [...]struct{ index int; oid ids.OID }{{0,fact.OID},{1,fact.Relation.ObjectID},{2,0},{4,function},{8,opposite},{9,constraint.Index.ObjectID},{10,constraint.OID}} {
		value, err := sqltype.OIDValue(in.Source.Columns[cell.index].Usage.Type, uint32(cell.oid))
		if err != nil { return nil, err }
		row[cell.index] = value
	}
	row[3] = sqltype.String(in.Source.Columns[3].Usage.Type, fact.Name)
	row[6] = sqltype.String(in.Source.Columns[6].Usage.Type, "O")
	for _, i := range [...]int{7,11,12} { row[i] = sqltype.Boolean(in.Source.Columns[i].Usage.Type, i == 7) }
	var err error
	row[5], err = sqltype.Integer(in.Source.Columns[5].Usage.Type, flags)
	if err != nil { return nil, err }
	row[13], err = sqltype.Integer(in.Source.Columns[13].Usage.Type, 0)
	if err != nil { return nil, err }
	row[14], err = sqltype.CatalogInt2Vector(in.Source.Columns[14].Usage.Type, nil)
	if err != nil { return nil, err }
	row[15], err = sqltype.Binary(in.Source.Columns[15].Usage.Type, "")
	if err != nil { return nil, err }
	return row, nil
}
