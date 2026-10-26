package pgcatalog

import (
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Only the represented builtins are classified here. catalogRelation is a
// virtual-source implementation kind, not a PostgreSQL table/view classifier.
func SummaryTable(oid ids.OID) bool {
	switch oid {
	case TypeOID, 1213, 1249, ids.RoleClassID, 1262, 2600, AMOID, 2605, 2612, 2613, TransformOID, TSParserOID, TSConfigOID, 3079, 2224, 2608, 2609, 2396, 2606, 2604, 1259, 2610, 2618, 2964, pgRangeOID, ids.ParameterClassID, 826:
		return true
	}
	return false
}

func captureTableViewSummary(in Input) ([][]sqltype.Value, error) {
	ctx, view, roles, source := in.Context, in.Catalog, in.Roles, in.Source
	var rows [][]sqltype.Value
	text := func(i int, value string) sqltype.Value { return sqltype.String(source.Columns[i].Usage.Type, value) }
	boolean := func(i int, value bool) sqltype.Value { return sqltype.Boolean(source.Columns[i].Usage.Type, value) }
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		builtinTable := relation.RelationKind == catalog.CatalogRelation && SummaryTable(relation.Address().ObjectID)
		builtinDefinition := ""
		if relation.RelationKind == catalog.CatalogRelation {
			builtinDefinition = SummaryViewDefinition(relation.Address().ObjectID)
		}
		if source.Address().ObjectID == 12003 {
			if relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && !builtinTable {
				continue
			}
		} else if source.Address().ObjectID == 12006 {
			if relation.RelationKind != catalog.MaterializedViewRelation {
				continue
			}
		} else if relation.RelationKind != catalog.ViewRelation && builtinDefinition == "" {
			continue
		}
		owner, err := roles.Descriptor(relation.Owner)
		if err != nil {
			return nil, err
		}
		schema := view.Get(relation.Namespace()).Name()
		if source.Address().ObjectID == 12003 {
			tablespace := sqltype.Null(source.Columns[3].Usage.Type)
			// Released catalog metadata, not fictitious native backing indexes/storage.
			// Represented shared catalogs belong to pg_global.
			if builtinTable && (relation.Address().ObjectID == ids.RoleClassID || relation.Address().ObjectID == 2396 || relation.Address().ObjectID == 1213 || relation.Address().ObjectID == 1262 || relation.Address().ObjectID == 2964 || relation.Address().ObjectID == ids.ParameterClassID) {
				tablespace = text(3, "pg_global")
			}
			rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.Name()), text(2, owner.Name), tablespace, boolean(4, relation.History.HasIndexes || builtinTable), boolean(5, false), boolean(6, false), boolean(7, relation.Security.RLSEnabled)})
		} else {
			// Stored and represented builtin views share the captured rendering
			// authority; builtin metadata never creates an executable payload.
			definition, err := in.Runtime.ViewDefinition(ctx, view, relation)
			if err != nil {
				return nil, err
			}
			definition = strings.TrimSuffix(definition, ";") + ";"
			if source.Address().ObjectID == 12006 {
				rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.Name()), text(2, owner.Name), sqltype.Null(source.Columns[3].Usage.Type), boolean(4, relation.History.HasIndexes), boolean(5, relation.Populated), text(6, definition)})
			} else {
				rows = append(rows, []sqltype.Value{text(0, schema), text(1, relation.Name()), text(2, owner.Name), text(3, definition)})
			}
		}
	}
	return rows, nil
}
