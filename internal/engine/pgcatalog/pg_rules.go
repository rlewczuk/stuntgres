package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const RulesOID ids.OID = 12023

// Rule definitions are public catalog metadata, not table-access authority.
// The runtime visits stored rules and excludes the exact SELECT implementation
// name before rendering; the producer owns their typed summary projection.
func capturePGRules(in Input) ([][]sqltype.Value, error) {
	rows := make([][]sqltype.Value, 0)
	text := func(i int, value string) sqltype.Value {
		return sqltype.String(in.Source.Columns[i].Usage.Type, value)
	}
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := in.Runtime.VisitRuleDefinitions(in.Context, in.Catalog, relation, func(rule catalog.RuleFact, definition string) error {
			if err := in.Context.Err(); err != nil {
				return err
			}
			namespace, ok := in.Catalog.Namespace(relation.Namespace())
			if !ok {
				return ids.ErrCatalogReplaced
			}
			rows = append(rows, []sqltype.Value{text(0, namespace.Name()), text(1, relation.Name()), text(2, rule.Name), text(3, definition)})
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
