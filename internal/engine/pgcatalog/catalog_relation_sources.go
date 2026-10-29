package pgcatalog

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGIndex(in Input) ([][]sqltype.Value, error) {
	ctx, view := in.Context, in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expected := [21]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int2, sqltype.Int2, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Int2Vector, sqltype.OidVector, sqltype.OidVector, sqltype.Int2Vector, sqltype.PgNodeTree, sqltype.PgNodeTree}
	if len(source.Columns) != len(expected) {
		return nil, fmt.Errorf("stuntgres: invalid index source shape")
	}
	for i, id := range expected {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign index source type")
		}
	}
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

	integer := func(i int, n int16) sqltype.Value {
		value, err := sqltype.Integer(source.Columns[i].Usage.Type, int64(n))
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
		if relation.SQLIndex != nil {
			facts, err := in.Runtime.IndexMetadata(ctx, view, relation, [6]*sqltype.Descriptor{source.Columns[15].Usage.Type, source.Columns[16].Usage.Type, source.Columns[17].Usage.Type, source.Columns[18].Usage.Type, source.Columns[19].Usage.Type, source.Columns[20].Usage.Type})
			if err != nil {
				return nil, err
			}
			rows = append(rows, []sqltype.Value{
				oid(0, uint32(facts.IndexRelID)), oid(1, uint32(facts.IndRelID)),
				integer(2, facts.IndNatts), integer(3, facts.IndNKeyAtts),
				boolean(4, facts.IndIsUnique), boolean(5, facts.IndNullsNotDistinct),
				boolean(6, facts.IndIsPrimary), boolean(7, facts.IndIsExclusion),
				boolean(8, facts.IndImmediate), boolean(9, facts.IndIsClustered),
				boolean(10, facts.IndIsValid), boolean(11, facts.IndCheckXmin),
				boolean(12, facts.IndIsReady), boolean(13, facts.IndIsLive), boolean(14, facts.IndIsReplIdent),
				facts.IndKey, facts.IndCollation, facts.IndClass, facts.IndOption, facts.IndexExpressions, facts.Predicate,
			})
		}
		if captureErr != nil {
			return nil, captureErr
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
	// Derive FK-side metadata once for this capture, not once per catalog row.
	var foreignKeyTriggers map[ids.ObjectAddress]bool
	for fact := range catalog.ForeignKeyTriggerFacts(view) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if foreignKeyTriggers == nil {
			foreignKeyTriggers = make(map[ids.ObjectAddress]bool)
		}
		foreignKeyTriggers[fact.Relation] = true
	}

	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.Keep.Rejects(ClassOIDColumn, func() (sqltype.Value, error) {
			return sqltype.OIDValue(in.Source.Columns[ClassOIDColumn].Usage.Type, uint32(relation.Address().ObjectID))
		}) || in.Keep.Rejects(ClassNameColumn, func() (sqltype.Value, error) {
			return sqltype.String(in.Source.Columns[ClassNameColumn].Usage.Type, relation.Name()), nil
		}) {
			continue
		}
		row, err := classRow(in, relation, relation.HasTriggers() || foreignKeyTriggers[relation.Address()])
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
		if rule := relation.ReturnRuleOID(); rule != 0 {
			rows = append(rows, []sqltype.Value{oid(0, uint32(rule)), text(1, "_RETURN"), oid(2, uint32(relation.Address().ObjectID)), text(3, "1"), text(4, "O"), boolean(5, true)})
		}
		if err := relation.VisitRules(func(rule catalog.RuleFact) error {
			rows = append(rows, []sqltype.Value{oid(0, uint32(rule.OID)), text(1, rule.Name), oid(2, uint32(relation.Address().ObjectID)), text(3, rule.Event), text(4, rule.Mode.String()), boolean(5, rule.Instead)})
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
	rows := make([][]sqltype.Value, 0)
	if in.RoleSettings == nil {
		return rows, nil
	}
	for scope, config := range in.RoleSettings.Rows() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		database, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(scope.Database))
		if err != nil {
			return nil, err
		}
		principal, err := sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(scope.Role.ID()))
		if err != nil {
			return nil, err
		}
		array, err := configArray(in.Source.Columns[2].Usage.Type, config)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{database, principal, array})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
