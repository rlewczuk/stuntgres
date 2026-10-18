package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const pgAttributeOID ids.OID = 1249

var pgAttributeTypeIDs = [...]sqltype.ID{26, 19, 26, 21, 21, 23, 21, 16, 18, 18, 18, 16, 16, 16, 18, 18, 16, 16, 21, 26, 21, 1034, 1009, 1009, 2277}

func relationAttributeMetadata(relation *relationDescriptor, slot int) attributeMetadata {
	column := relation.Columns[slot]
	attribute := relation.Attributes[slot]
	metadata := attributeMetadata{Name: column.Name, TypeID: uint32(column.Usage.Type.ID()), Number: attribute, Facts: column.Usage.Type.AttributeFacts(), Typmod: column.Usage.Typmod, Dimensions: column.DeclaredDimensions, Collation: column.Usage.Collation.ID, NotNull: !column.Nullable, Local: true}
	_, metadata.HasDefault = relation.Defaults[attribute]
	if definition, ok := relation.Generations[attribute]; ok {
		metadata.HasDefault = true
		metadata.Generated = "s"
		if definition.Kind == generationVirtual {
			metadata.Generated = "v"
		}
	}
	if definition, ok := relation.Identities[attribute]; ok {
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
	if relation.RelationKind == viewRelation || relation.RelationKind == indexRelation {
		metadata.NotNull = false
	}
	if relation.SQLIndex != nil {
		metadata.HasDefault, metadata.Identity, metadata.Generated = false, "", ""
		if slot >= len(relation.SQLIndex.Keys) {
			metadata.Collation = 0
		}
	}
	if relation.RelationKind == catalogRelation {
		fact := relation.CatalogAttributes[slot]
		metadata.NotNull, metadata.Dimensions, metadata.Collation = fact.NotNull, fact.Dimensions, fact.Collation
	}
	return metadata
}

func hasSystemAttributes(catalog CatalogRead, relation *relationDescriptor) bool {
	switch pgClassKind(catalog, relation) {
	case "r", "p", "m", "S", "f":
		return true
	}
	return false
}

// Caller owns e.mu. Capture uses exactly the statement's selected relation and
// paired role roots, not the live catalog, source origins or stored row data.
func (e *Engine) capturePGAttribute(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.Owner() != e.catalog.owner() || catalog.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgAttributeOID}) || ref.Domain() != e.catalog.Domain || ref.Epoch != catalog.State().Epoch() || ref.Address != source.Address() || catalog.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign attribute source authority")
	}
	if !roles.BelongsTo(&e.authorizationDomain) {
		return nil, fmt.Errorf("stuntgres: foreign attribute ACL authority")
	}
	if len(source.Columns) != len(pgAttributeTypeIDs) {
		return nil, fmt.Errorf("stuntgres: invalid attribute source shape")
	}
	for i, id := range pgAttributeTypeIDs {
		owned, ok := catalog.LookupType(id)
		if !ok || source.Columns[i].Usage.Type != owned {
			return nil, fmt.Errorf("stuntgres: foreign attribute source type")
		}
	}
	if !source.Columns[24].Usage.CatalogNullUsage() {
		return nil, fmt.Errorf("stuntgres: invalid attribute NULL carrier")
	}
	count := 0
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			count += len(relation.Columns) + len(relation.DroppedAttributes)
			if hasSystemAttributes(catalog, relation) {
				count += 6
			}
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for _, descriptor := range catalog.Descriptors() {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		appendRow := func(metadata attributeMetadata) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			row, err := e.pgAttributeRow(roles, source, relation, metadata)
			if err != nil {
				return err
			}
			rows = append(rows, row)
			return nil
		}
		for slot := range relation.Columns {
			if err := appendRow(relationAttributeMetadata(relation, slot)); err != nil {
				return nil, err
			}
		}
		for _, metadata := range relation.DroppedAttributes {
			if err := appendRow(metadata); err != nil {
				return nil, err
			}
		}
		if hasSystemAttributes(catalog, relation) {
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
				metadata := attributeMetadata{Name: system.name, TypeID: system.oid, Number: system.number, Facts: sqltype.PostgreSQLAttributeFacts{Length: system.length, ByValue: system.byval, Alignment: system.align, Storage: "p"}, Typmod: -1, NotNull: true, Local: true}
				if err := appendRow(metadata); err != nil {
					return nil, err
				}
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

func (e *Engine) pgAttributeRow(roles authorization.Read, source, relation *relationDescriptor, metadata attributeMetadata) ([]sqltype.Value, error) {
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
		if value, exists := relation.ColumnACL[metadata.Number]; exists {
			acl = checked(e.pgClassACL(roles, source.Columns[21].Usage.Type, value))
		}
	}
	row := []sqltype.Value{
		oid(0, uint32(relation.Address().ObjectID)), text(1, metadata.Name), oid(2, metadata.TypeID), integer(3, int64(metadata.Facts.Length)), integer(4, int64(metadata.Number)), integer(5, int64(metadata.Typmod)), integer(6, int64(metadata.Dimensions)), boolean(7, metadata.Facts.ByValue), text(8, metadata.Facts.Alignment), text(9, metadata.Facts.Storage), text(10, ""), boolean(11, metadata.NotNull), boolean(12, metadata.HasDefault), boolean(13, false), text(14, metadata.Identity), text(15, metadata.Generated), boolean(16, metadata.Dropped), boolean(17, metadata.Local), integer(18, metadata.Inheritance), oid(19, metadata.Collation), null(20), acl, null(22), null(23), sqltype.CatalogResultNull(source.Columns[24].Usage),
	}
	if relation.Foreign != nil && metadata.Number > 0 && !metadata.Dropped {
		row[23] = checked(foreignOptionsValue(source.Columns[23].Usage.Type, relation.Foreign.Columns[metadata.Number]))
	}
	return row, failure
}
