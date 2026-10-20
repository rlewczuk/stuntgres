package pgcatalog

import (
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func capturePGIndex(in Input) ([][]sqltype.Value, error) {
	ctx, view := in.Context, in.Catalog
	source := in.Source
	var relations []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	var captureErr error

	oid := func(i int, n uint32) sqltype.Value {
		value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}

	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.Columns[i].Usage.Type, b) }
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if definition := relation.SQLIndex; definition != nil {
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.Address().ObjectID)), oid(1, uint32(definition.Parent.ObjectID)), boolean(2, definition.Unique), boolean(3, definition.Constraint != nil && definition.Constraint.Primary), boolean(4, definition.Valid), boolean(5, definition.Ready), boolean(6, definition.Live), boolean(7, definition.NullsNotDistinct)})
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	return rows, nil
}

func capturePGAttrdef(in Input) ([][]sqltype.Value, error) {
	ctx, view := in.Context, in.Catalog
	source := in.Source
	var relations []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	var captureErr error
	integer := func(i int, n int64) sqltype.Value {
		value, err := sqltype.Integer(source.Columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value {
		value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}

	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for attribute, definition := range relation.ColumnMeta.Defaults {
			tree, err := in.Runtime.ExpressionTree(source.Columns[3].Usage.Type, definition.Expression)
			if err != nil {
				return nil, err
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.Address().ObjectID)), integer(2, int64(attribute)), tree})
		}
		for attribute, definition := range relation.ColumnMeta.Generations {
			tree, err := in.Runtime.ExpressionTree(source.Columns[3].Usage.Type, definition.Expression)
			if err != nil {
				return nil, err
			}
			rows = append(rows, []sqltype.Value{oid(0, uint32(definition.OID)), oid(1, uint32(relation.Address().ObjectID)), integer(2, int64(attribute)), tree})
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	return rows, nil
}

func capturePGClass(in Input) ([][]sqltype.Value, error) {
	ctx, view := in.Context, in.Catalog
	var relations []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))

	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := ClassRow(in, relation)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func capturePGRewrite(in Input) ([][]sqltype.Value, error) {
	ctx, view := in.Context, in.Catalog
	source := in.Source
	var relations []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	var captureErr error

	oid := func(i int, n uint32) sqltype.Value {
		value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, n)
		if err != nil && captureErr == nil {
			captureErr = err
		}
		return value
	}
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.Columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.Columns[i].Usage.Type, b) }
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation.View != nil {
			rows = append(rows, []sqltype.Value{oid(0, uint32(relation.View.RuleOID())), text(1, "_RETURN"), oid(2, uint32(relation.Address().ObjectID)), text(3, "1"), text(4, "O"), boolean(5, true)})
		}
		if err := in.Runtime.VisitRules(relation, func(rule RuleFact) error {
			rows = append(rows, []sqltype.Value{oid(0, uint32(rule.OID)), text(1, rule.Name), oid(2, uint32(relation.Address().ObjectID)), text(3, rule.Event), text(4, "O"), boolean(5, rule.Instead)})
			return nil
		}); err != nil {
			return nil, err
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	return rows, nil
}

func capturePGDBRoleSetting(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	// Session defaults and overrides are not persistent catalog rows.
	return [][]sqltype.Value{}, nil
}
