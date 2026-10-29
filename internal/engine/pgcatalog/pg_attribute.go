package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const AttributeOID ids.OID = 1249

var pgAttributeTypeIDs = [...]sqltype.ID{26, 19, 26, 21, 21, 23, 21, 16, 18, 18, 18, 16, 16, 16, 18, 18, 16, 16, 21, 26, 21, 1034, 1009, 1009, 2277}

func AttributeMetadata(relation *catalog.Relation, slot int) catalog.AttributeMetadata {
	column := relation.Columns[slot]
	attribute := relation.Attributes[slot]
	metadata := catalog.AttributeMetadata{Name: column.Name, TypeID: uint32(column.Usage.Type.ID()), Number: attribute, Facts: column.Usage.Type.AttributeFacts(), Typmod: column.Usage.Typmod, Dimensions: column.DeclaredDimensions, Collation: column.Usage.Collation.ID, NotNull: !column.Nullable, Local: true}
	properties := relation.ColumnStorage(slot)
	metadata.Facts.Storage, metadata.Compression = properties.Storage, properties.Compression
	_, metadata.HasDefault = relation.ColumnMeta.Defaults[attribute]
	if definition, ok := relation.ColumnMeta.Generations[attribute]; ok {
		metadata.HasDefault = true
		metadata.Generated = "s"
		if definition.Kind == catalog.GenerationVirtual {
			metadata.Generated = "v"
		}
	}
	if definition, ok := relation.ColumnMeta.Identities[attribute]; ok {
		metadata.Identity = "d"
		if definition.Mode == parser.IdentityAlways {
			metadata.Identity = "a"
		}
	}
	if relation.Inheritance != nil {
		if provenance, ok := relation.Inheritance.Columns[attribute]; ok {
			metadata.Local, metadata.Inheritance = provenance.Local, int64(len(provenance.Origins))
		}
	}
	if relation.Membership != nil {
		metadata.Local, metadata.Inheritance = false, 1
	}
	if relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.IndexRelation {
		metadata.NotNull = false
	}
	if relation.SQLIndex != nil {
		metadata.HasDefault, metadata.Identity, metadata.Generated = false, "", ""
		if slot >= len(relation.SQLIndex.Keys) {
			metadata.Collation = 0
		}
	}
	if relation.RelationKind == catalog.CatalogRelation {
		fact := relation.ColumnMeta.CatalogAttributes[slot]
		metadata.NotNull, metadata.Dimensions, metadata.Collation = fact.NotNull, fact.Dimensions, fact.Collation
	}
	return metadata
}

func HasSystemAttributes(_ catalog.Read, relation *catalog.Relation) bool {
	return relation.HasSystemAttributes()
}

// Caller owns e.mu. Capture uses exactly the statement's selected relation and
// paired role roots, not the live catalog, source origins or stored row data.
func capturePGAttribute(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	ref := in.Ref
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: AttributeOID}) || ref.Domain() != in.Owner.Domain || ref.Epoch != view.State().Epoch() || ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign attribute source authority")
	}
	if !roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign attribute ACL authority")
	}
	if len(source.Columns) != len(pgAttributeTypeIDs) {
		return nil, fmt.Errorf("stuntgres: invalid attribute source shape")
	}
	for i, id := range pgAttributeTypeIDs {
		owned, ok := view.LookupType(id)
		if !ok || source.Columns[i].Usage.Type != owned {
			return nil, fmt.Errorf("stuntgres: foreign attribute source type")
		}
	}
	if !source.Columns[24].Usage.CatalogNullUsage() {
		return nil, fmt.Errorf("stuntgres: invalid attribute NULL carrier")
	}
	// Every row of a relation shares its attrelid cell.
	rejected := func(relation *catalog.Relation) bool {
		return in.Keep.Rejects(AttributeRelationColumn, func() (sqltype.Value, error) {
			return sqltype.OIDValue(source.Columns[AttributeRelationColumn].Usage.Type, uint32(relation.Address().ObjectID))
		})
	}
	count := 0
	for _, relation := range view.Relations() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if rejected(relation) {
			continue
		}
		count += len(relation.Columns) + len(relation.ColumnMeta.Dropped)
		if HasSystemAttributes(view, relation) {
			count += 6
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for _, relation := range view.Relations() {
		if rejected(relation) {
			continue
		}
		appendRow := func(metadata catalog.AttributeMetadata) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			row, err := AttributeRow(in.Owner, roles, source, relation, metadata)
			if err != nil {
				return err
			}
			rows = append(rows, row)
			return nil
		}
		for slot := range relation.Columns {
			metadata := AttributeMetadata(relation, slot)
			if definition := relation.SQLIndex; definition != nil && slot < len(definition.Keys) {
				if storage := definition.Keys[slot].Class.StorageOID(); storage != 0 {
					typ, exists := view.LookupType(sqltype.ID(storage))
					if !exists {
						return nil, fmt.Errorf("stuntgres: missing index storage type")
					}
					metadata.TypeID, metadata.Facts, metadata.Typmod = storage, typ.AttributeFacts(), -1
					metadata.Dimensions = 0
					metadata.Facts.Storage = relation.ColumnStorage(slot).Storage
				}
			}
			if err := appendRow(metadata); err != nil {
				return nil, err
			}
		}
		for _, metadata := range relation.ColumnMeta.Dropped {
			if err := appendRow(metadata); err != nil {
				return nil, err
			}
		}
		if HasSystemAttributes(view, relation) {
			for _, system := range [...]struct {
				name   string
				oid    uint32
				number int32
				length int16
				byval  bool
				align  string
			}{
				{"tableoid", 26, -6, 4, true, "i"}, {"cmax", 29, -5, 4, true, "i"}, {"xmax", 28, -4, 4, true, "i"}, {"cmin", 29, -3, 4, true, "i"}, {"xmin", 28, -2, 4, true, "i"}, {"ctid", 27, -1, 6, false, "s"},
			} {
				metadata := catalog.AttributeMetadata{Name: system.name, TypeID: system.oid, Number: system.number, Facts: sqltype.PostgreSQLAttributeFacts{Length: system.length, ByValue: system.byval, Alignment: system.align, Storage: "p"}, Typmod: -1, NotNull: true, Local: true}
				if err := appendRow(metadata); err != nil {
					return nil, err
				}
			}
		}
	}
	return rows, nil
}

func AttributeRow(owner *catalog.Owner, roles authorization.Read, source, relation *catalog.Relation, metadata catalog.AttributeMetadata) ([]sqltype.Value, error) {
	var failure error
	checked := func(value sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value { return checked(sqltype.OIDValue(source.Columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) sqltype.Value { return checked(sqltype.Integer(source.Columns[i].Usage.Type, n)) }
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.Columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.Columns[i].Usage.Type, b) }
	null := func(i int) sqltype.Value { return sqltype.Null(source.Columns[i].Usage.Type) }
	acl := null(21)
	if metadata.Number > 0 && !metadata.Dropped {
		if value, exists := relation.Security.ColumnACL[metadata.Number]; exists {
			acl = checked(ClassACL(owner, roles, source.Columns[21].Usage.Type, value))
		}
	}
	row := []sqltype.Value{
		oid(0, uint32(relation.Address().ObjectID)), text(1, metadata.Name), oid(2, metadata.TypeID), integer(3, int64(metadata.Facts.Length)), integer(4, int64(metadata.Number)), integer(5, int64(metadata.Typmod)), integer(6, int64(metadata.Dimensions)), boolean(7, metadata.Facts.ByValue), text(8, metadata.Facts.Alignment), text(9, metadata.Facts.Storage), text(10, metadata.Compression), boolean(11, metadata.NotNull), boolean(12, metadata.HasDefault), boolean(13, false), text(14, metadata.Identity), text(15, metadata.Generated), boolean(16, metadata.Dropped), boolean(17, metadata.Local), integer(18, metadata.Inheritance), oid(19, metadata.Collation), null(20), acl, null(22), null(23), sqltype.CatalogResultNull(source.Columns[24].Usage),
	}
	if metadata.Number > 0 && !metadata.Dropped {
		configuration := relation.ColumnMeta.Statistics[metadata.Number]
		if configuration.TargetSet {
			row[20] = integer(20, int64(configuration.Target))
		}
		if len(configuration.Options) != 0 {
			row[22] = checked(foreignOptionsValue(source.Columns[22].Usage.Type, configuration.Options))
		}
	}
	if definition := relation.SQLIndex; definition != nil && metadata.Number > 0 && !metadata.Dropped && int(metadata.Number) <= len(definition.Keys) {
		if options := definition.Keys[metadata.Number-1].ClassOptions; options.Explicit {
			row[22] = checked(foreignOptionsValue(source.Columns[22].Usage.Type, []catalog.ForeignOption{{Name: "siglen", Value: options.Text}}))
		}
	}
	if relation.Foreign != nil && metadata.Number > 0 && !metadata.Dropped {
		row[23] = checked(foreignOptionsValue(source.Columns[23].Usage.Type, relation.Foreign.Columns[metadata.Number]))
	}
	return row, failure
}
