package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const pgAttributeOID OID = 1249

var pgAttributeTypeIDs = [...]sqltype.ID{26, 19, 26, 21, 21, 23, 21, 16, 18, 18, 18, 16, 16, 16, 18, 18, 16, 16, 21, 26, 21, 1034, 1009, 1009, 2277}

// Scalar-only history survives removal of the original type and its dependency.
// Published maps and entries are immutable; no live query or codec sees them.
type attributeMetadata struct {
	name                                string
	typeID                              uint32
	number                              int32
	facts                               sqltype.PostgreSQLAttributeFacts
	typmod                              int32
	dimensions                          int16
	collation                           uint32
	notNull, hasDefault, dropped, local bool
	inheritance                         int64
	identity, generated                 string
}

func relationAttributeMetadata(relation *relationDescriptor, slot int) attributeMetadata {
	column := relation.columns[slot]
	attribute := relation.attributes[slot]
	metadata := attributeMetadata{name: column.Name, typeID: uint32(column.Usage.Type.ID()), number: attribute, facts: column.Usage.Type.AttributeFacts(), typmod: column.Usage.Typmod, dimensions: column.DeclaredDimensions, collation: column.Usage.Collation.ID, notNull: !column.Nullable, local: true}
	_, metadata.hasDefault = relation.defaults[attribute]
	if definition, ok := relation.generations[attribute]; ok {
		metadata.hasDefault = true
		metadata.generated = "s"
		if definition.kind == generationVirtual {
			metadata.generated = "v"
		}
	}
	if definition, ok := relation.identities[attribute]; ok {
		metadata.identity = "d"
		if definition.mode == parser.IdentityAlways {
			metadata.identity = "a"
		}
	}
	if relation.inheritance != nil {
		if provenance, ok := relation.inheritance.columns[attribute]; ok {
			metadata.local, metadata.inheritance = provenance.local, int64(len(provenance.origins))
		}
	}
	if relation.membership != nil {
		metadata.local, metadata.inheritance = false, 1
	}
	if relation.relationKind == viewRelation || relation.relationKind == indexRelation {
		metadata.notNull = false
	}
	if relation.sqlIndex != nil {
		metadata.hasDefault, metadata.identity, metadata.generated = false, "", ""
		if slot >= len(relation.sqlIndex.keys) {
			metadata.collation = 0
		}
	}
	if relation.relationKind == catalogRelation {
		fact := relation.catalogAttributes[slot]
		metadata.notNull, metadata.dimensions, metadata.collation = fact.notNull, fact.dimensions, fact.collation
	}
	return metadata
}

func hasSystemAttributes(catalog CatalogRead, relation *relationDescriptor) bool {
	switch pgClassKind(catalog, relation) {
	case "r", "p", "m", "S":
		return true
	}
	return false
}

// Caller owns e.mu. Capture uses exactly the statement's selected relation and
// paired role roots, not the live catalog, source origins or stored row data.
func (e *Engine) capturePGAttribute(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner != e || catalog.state == nil || source == nil || source.address != (ObjectAddress{ClassID: RelationClassID, ObjectID: pgAttributeOID}) || ref.owner != e.owner || ref.Epoch != catalog.state.epoch || ref.Address != source.address || catalog.state.descriptors[source.address] != source {
		return nil, fmt.Errorf("stuntgres: foreign attribute source authority")
	}
	if !roles.BelongsTo(&e.authorizationDomain) {
		return nil, fmt.Errorf("stuntgres: foreign attribute ACL authority")
	}
	if len(source.columns) != len(pgAttributeTypeIDs) {
		return nil, fmt.Errorf("stuntgres: invalid attribute source shape")
	}
	for i, id := range pgAttributeTypeIDs {
		owned, ok := catalog.LookupType(id)
		if !ok || source.columns[i].Usage.Type != owned {
			return nil, fmt.Errorf("stuntgres: foreign attribute source type")
		}
	}
	if !source.columns[24].Usage.CatalogNullUsage() {
		return nil, fmt.Errorf("stuntgres: invalid attribute NULL carrier")
	}
	count := 0
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			count += len(relation.columns) + len(relation.droppedAttributes)
			if hasSystemAttributes(catalog, relation) {
				count += 6
			}
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for _, descriptor := range catalog.state.descriptors {
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
		for slot := range relation.columns {
			if err := appendRow(relationAttributeMetadata(relation, slot)); err != nil {
				return nil, err
			}
		}
		for _, metadata := range relation.droppedAttributes {
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
				metadata := attributeMetadata{name: system.name, typeID: system.oid, number: system.number, facts: sqltype.PostgreSQLAttributeFacts{Length: system.length, ByValue: system.byval, Alignment: system.align, Storage: "p"}, typmod: -1, notNull: true, local: true}
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
	oid := func(i int, n uint32) sqltype.Value { return checked(sqltype.OIDValue(source.columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) sqltype.Value { return checked(sqltype.Integer(source.columns[i].Usage.Type, n)) }
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.columns[i].Usage.Type, b) }
	null := func(i int) sqltype.Value { return sqltype.Null(source.columns[i].Usage.Type) }
	acl := null(21)
	if metadata.number > 0 && !metadata.dropped {
		if value, exists := relation.columnACL[metadata.number]; exists {
			acl = checked(e.pgClassACL(roles, source.columns[21].Usage.Type, value))
		}
	}
	row := []sqltype.Value{
		oid(0, uint32(relation.address.ObjectID)), text(1, metadata.name), oid(2, metadata.typeID), integer(3, int64(metadata.facts.Length)), integer(4, int64(metadata.number)), integer(5, int64(metadata.typmod)), integer(6, int64(metadata.dimensions)), boolean(7, metadata.facts.ByValue), text(8, metadata.facts.Alignment), text(9, metadata.facts.Storage), text(10, ""), boolean(11, metadata.notNull), boolean(12, metadata.hasDefault), boolean(13, false), text(14, metadata.identity), text(15, metadata.generated), boolean(16, metadata.dropped), boolean(17, metadata.local), integer(18, metadata.inheritance), oid(19, metadata.collation), null(20), acl, null(22), null(23), sqltype.CatalogResultNull(source.columns[24].Usage),
	}
	return row, failure
}
