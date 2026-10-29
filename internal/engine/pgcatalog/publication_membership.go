package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const PublicationRelationOID ids.OID = 6106

// Native source identity, independent of PostgreSQL's initdb-generated view OID.
const PublicationTablesOID ids.OID = 12205

// PublicationTableFact borrows direct metadata for one presented relation from
// the runtime's captured catalog. The producer only owns typed serialization.
type PublicationTableFact struct {
	Relation *catalog.Relation
	Columns  []int32
	Filter   catalog.PolicyPayload
}

func validatePublicationSource(in Input, oid ids.OID) error {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return fmt.Errorf("stuntgres: invalid publication membership source")
	}
	if err := in.Context.Err(); err != nil {
		return err
	}
	return in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, oid)
}
func capturePGPublicationRelation(in Input) ([][]sqltype.Value, error) {
	if err := validatePublicationSource(in, PublicationRelationOID); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, m := range in.Catalog.PublicationRelations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.PublicationRelationProvider{}).Validate(in.Catalog, m); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 5)
		for i, oid := range []ids.OID{m.Address().ObjectID, m.Publication.ObjectID, m.Relation.ObjectID} {
			var err error
			row[i], err = sqltype.OIDValue(in.Source.Columns[i].Usage.Type, uint32(oid))
			if err != nil {
				return nil, err
			}
		}
		r, _ := in.Catalog.Relation(m.Relation)
		var err error
		row[3], err = in.Runtime.PolicyTree(in.Context, in.Catalog, r, in.Source.Columns[3].Usage.Type, m.Filter)
		if err != nil {
			return nil, err
		}
		array := in.Source.Columns[4].Usage.Type
		row[4] = sqltype.Null(array)
		if m.ColumnsSpecified {
			values := make([]sqltype.Value, len(m.Columns))
			for i, sub := range m.Columns {
				values[i], err = sqltype.Integer(array.Element(), int64(sub))
				if err != nil {
					return nil, err
				}
			}
			row[4], err = sqltype.OwnedCatalogVectorValue(array, values)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
func capturePGPublicationTables(in Input) ([][]sqltype.Value, error) {
	if err := validatePublicationSource(in, PublicationTablesOID); err != nil {
		return nil, err
	}
	for _, m := range in.Catalog.PublicationRelations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.PublicationRelationProvider{}).Validate(in.Catalog, m); err != nil {
			return nil, err
		}
	}
	for _, m := range in.Catalog.PublicationNamespaces() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.PublicationNamespaceProvider{}).Validate(in.Catalog, m); err != nil {
			return nil, err
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, p := range in.Catalog.Publications() {
		if err := (catalog.PublicationProvider{}).Validate(in.Catalog, p); err != nil {
			return nil, err
		}
		err := in.Runtime.VisitPublicationTables(in.Context, in.Catalog, p, func(item PublicationTableFact) error {
			if err := in.Context.Err(); err != nil {
				return err
			}
			ns := in.Catalog.MustNamespace(item.Relation.Namespace())
			row := make([]sqltype.Value, 5)
			for i, text := range []string{p.Name(), ns.Name(), item.Relation.Name()} {
				row[i] = sqltype.String(in.Source.Columns[i].Usage.Type, text)
			}
			array := in.Source.Columns[3].Usage.Type
			row[3] = sqltype.Null(array)
			if len(item.Columns) > 0 {
				values := make([]sqltype.Value, len(item.Columns))
				for i, sub := range item.Columns {
					slot := item.Relation.IndexForAttribute(sub)
					if slot < 0 {
						return ids.ErrCatalogReplaced
					}
					values[i] = sqltype.String(array.Element(), item.Relation.Columns[slot].Name)
				}
				var err error
				row[3], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(values)), Lower: 1}}, values)
				if err != nil {
					return err
				}
			}
			row[4] = sqltype.Null(in.Source.Columns[4].Usage.Type)
			if item.Filter != nil {
				text, e := in.Runtime.PolicyDisplay(in.Context, in.Catalog, item.Relation, item.Filter)
				if e != nil {
					return e
				}
				row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, text)
			}
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return rows, in.Context.Err()
}
