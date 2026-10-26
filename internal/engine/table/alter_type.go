package table

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type AlterTypePreparation struct {
	Usage              sqltype.Usage
	Transformation     index.ColumnTransformation
	DeclaredDimensions int16
}

// Phase-one lookup uses the statement-original descriptor, before ADD/DROP passes.
func AlterTypeColumn(original *catalog.Relation, name string) (int, error) {
	slot, ok := original.Index[name]
	if !ok {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, original.Name()))
	}
	return slot, nil
}

// alterTypeUsing is the storage transformation's input: the bound USING
// expression, or the old column itself.
func alterTypeUsing(h AlterHost, auth ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction, usage sqltype.Usage) (expr.Expr, []catalog.ExpressionDependency, error) {
	if action.Using == nil {
		source := original.Index[action.Column.Text]
		return plan.ColumnExpr{Info: expr.Info{Usage: original.Columns[source].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: source}.WithGeneration(original, source, 0), nil, nil
	}
	return h.BindAlterTypeUsing(auth, original, ref, action.Using, usage)
}

// Complete phase-one TYPE work against the immutable original row. The caller
// holds Engine.mu; neither row evaluation nor candidate mutation belongs here.
// suppliedUsage is used only by typed propagation of an already resolved type.
func PrepareAlterType(h AlterHost, auth ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (AlterTypePreparation, error) {
	prepared, err := AdmitAlterType(h, auth, original, ref, a, suppliedUsage)
	if err != nil {
		return AlterTypePreparation{}, err
	}
	return CompleteAlterType(h, auth, original, a, prepared)
}

// Admission resolves the target and binds USING once, before sequence AS.
// Its raw expression is replaced by the completed storage transformation.
func AdmitAlterType(h AlterHost, auth ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (AlterTypePreparation, error) {
	fail := func(err error) (AlterTypePreparation, error) {
		return AlterTypePreparation{}, err
	}
	source, err := AlterTypeColumn(original, a.Column.Text)
	if err != nil {
		return fail(err)
	}
	if ref.Epoch != h.LiveEpoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	if _, generated := original.ColumnMeta.Generations[original.Attributes[source]]; generated {
		if a.Using != nil {
			diagnostic := expr.ErrorAt("42611", "cannot specify USING when altering type of generated column", a.Column.Pos).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, a.Column.Text)
			return fail(diagnostic)
		}
		return fail(sqlError("0A000", "altering type of a generated column is unsupported"))
	}
	var usage sqltype.Usage
	if suppliedUsage != nil {
		usage = *suppliedUsage
	} else {
		usage, err = h.ResolveType(a.TypeName)
		if err != nil {
			return fail(err)
		}
	}
	if err := sqltype.CheckStoredColumn(a.Column.Text, usage); err != nil {
		return fail(err)
	}
	usage, err = h.ResolveColumnCollation(usage, a.Collation)
	if err != nil {
		return fail(err)
	}
	prepared := AlterTypePreparation{Usage: usage, DeclaredDimensions: int16(len(a.TypeName.ArrayBounds))}
	if original.RelationKind == catalog.CompositeRelation {
		return prepared, nil
	}
	if original.RelationKind == catalog.ForeignRelation {
		if a.Using != nil {
			if _, _, err := alterTypeUsing(h, auth, original, ref, a, usage); err != nil {
				return fail(err)
			}
			return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a table`, original.Name())))
		}
		return prepared, nil
	}
	node, dependencies, err := alterTypeUsing(h, auth, original, ref, a, usage)
	if err != nil {
		return fail(err)
	}
	prepared.Transformation = index.ColumnTransformation{Attribute: original.Attributes[source], Expression: node, Dependencies: dependencies}
	return prepared, nil
}

// Engine.mu remains held. No expression is evaluated during completion.
func CompleteAlterType(h AlterHost, auth ddl.Authority, original *catalog.Relation, a parser.AlterTableAction, prepared AlterTypePreparation) (AlterTypePreparation, error) {
	fail := func(err error) (AlterTypePreparation, error) {
		return AlterTypePreparation{}, err
	}
	if original.RelationKind == catalog.CompositeRelation || original.RelationKind == catalog.ForeignRelation {
		return prepared, nil
	}
	usage := prepared.Usage
	node := prepared.Transformation.Expression
	input, annotations := analyze.DomainCoercionInput(node, usage)
	info, err := analyze.StorageCoercionInfo(h.AccessView(auth).Catalog, expr.Info{Usage: usage, Property: input.Properties()}, input)
	if err != nil {
		return fail(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
			d = sqlError("42804", fmt.Sprintf(`column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())).(*sqlstate.Error)
			declaration, _ := usage.Declaration()
			if usage.Type.ID() == sqltype.VarChar || usage.Type.ID() == sqltype.BPChar {
				declaration = usage.Type.DiagnosticName() + strings.TrimPrefix(declaration, usage.Type.SQLName())
			}
			identifier := a.Column.Text
			if parser.IdentifierNeedsQuotes(identifier) {
				identifier = catalog.QuoteIdentifier(identifier)
			}
			d.Hint = fmt.Sprintf(`You might need to specify "USING %s::%s".`, identifier, strings.ToLower(declaration))
			if a.Using != nil {
				d.Message = fmt.Sprintf(`result of USING clause for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())
				d.Hint = "You might need to add an explicit cast."
			}
			return fail(d)
		}
		return fail(err)
	}
	if coercionPlan.RequiresCatalogOutput() {
		if err := h.CaptureCommentSnapshot(); err != nil {
			return fail(err)
		}
	}
	info.Property = analyze.CoercionProperties(input, coercionPlan)
	node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	node, err = analyze.StorageDestination(node, usage, info.Pos)
	if err != nil {
		return fail(err)
	}
	source := original.Index[a.Column.Text]
	oldUsage := original.Columns[source].Usage
	prepared.Transformation.Expression = node
	prepared.Transformation.Rewrite = a.Using != nil || oldUsage.Type != usage.Type || oldUsage.Typmod != usage.Typmod
	prepared.Transformation.EventRewrite = ColumnTypeEventRewrite(node, original, source, h.Input().Zone)
	return prepared, nil
}

// Candidate-only mutation consumes the completed phase-one result.
func AlterTypeCandidate(h AlterHost, auth ddl.Authority, original, next *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, prepared AlterTypePreparation) (*catalog.Relation, index.ColumnTransformation, error) {
	view := h.AccessView(auth)
	fail := func(err error) (*catalog.Relation, index.ColumnTransformation, error) {
		return nil, index.ColumnTransformation{}, err
	}
	slot, err := AlterTypeColumn(next, a.Column.Text)
	if err != nil {
		return fail(err)
	}
	for _, attribute := range next.Attributes {
		definition, generated := next.ColumnMeta.Generations[attribute]
		if !generated {
			continue
		}
		for _, dependency := range definition.Dependencies {
			if dependency.Address.ClassID == next.Address().ClassID && dependency.Address.ObjectID == next.Address().ObjectID && dependency.Address.SubID == next.Attributes[slot] {
				diagnostic := sqlError("0A000", "cannot alter type of a column used by a generated column").(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf(`Column "%s" is used by generated column "%s".`, a.Column.Text, next.Columns[next.IndexForAttribute(attribute)].Name)
				return fail(diagnostic)
			}
		}
	}
	source := original.Index[a.Column.Text]
	oldUsage := original.Columns[source].Usage
	current := next.Columns[slot].Usage
	if current.Type.ID() != oldUsage.Type.ID() || current.Typmod != oldUsage.Typmod {
		return fail(sqlError("0A000", fmt.Sprintf(`cannot alter type of column "%s" twice`, a.Column.Text)))
	}
	if ref.Epoch != h.LiveEpoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	target := ref.Address
	target.SubID = original.Attributes[source]
	if err := h.TypeChangeDependents(auth, target, a.Column.Text); err != nil {
		return fail(err)
	}
	usage := prepared.Usage
	if original.RelationKind == catalog.CompositeRelation {
		if usage == current && next.Columns[slot].DeclaredDimensions == prepared.DeclaredDimensions {
			return next, index.ColumnTransformation{}, nil
		}
		replacement := *next
		replacement.Columns = append([]catalog.Column(nil), next.Columns...)
		replacement.Columns[slot].Usage = usage
		replacement.Columns[slot].DeclaredDimensions = prepared.DeclaredDimensions
		return &replacement, index.ColumnTransformation{}, nil
	}
	attribute := original.Attributes[source]
	transformation := prepared.Transformation
	dimensions := prepared.DeclaredDimensions
	if usage == current && next.Columns[slot].DeclaredDimensions == dimensions {
		return next, transformation, nil
	}
	replacement := *next
	replacement.Columns = append([]catalog.Column(nil), next.Columns...)
	replacement.Columns[slot].Usage = usage
	replacement.Columns[slot].DeclaredDimensions = dimensions
	if usage == current {
		return &replacement, transformation, nil
	}
	if definition, exists := next.ColumnMeta.Defaults[attribute]; exists {
		expression := definition.Expression
		for {
			wrapper, ok := expression.(plan.CoercionExpr)
			if !ok || wrapper.Mode == sqltype.Explicit {
				break
			}
			expression = wrapper.Child
		}
		input, annotations := analyze.DomainCoercionInput(expression, usage)
		info, err := analyze.StorageCoercionInfo(view.Catalog, expr.Info{Usage: usage, Property: input.Properties()}, input)
		if err != nil {
			return fail(err)
		}
		defaultPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return fail(sqlError("42804", fmt.Sprintf(`default for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())))
		}
		info.Property = analyze.CoercionProperties(input, defaultPlan)
		expression = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: defaultPlan, Mode: sqltype.Assignment})
		expression, err = analyze.StorageDestination(expression, usage, info.Pos)
		if err != nil {
			return fail(err)
		}
		definition.Dependencies = nil
		definition.CatalogSnapshot = false
		_, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot}).SQL(h.Context(), view.Catalog, expression)
		if err != nil {
			return fail(err)
		}
		replacement.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(next.ColumnMeta.Defaults))
		for id, d := range next.ColumnMeta.Defaults {
			replacement.ColumnMeta.Defaults[id] = d
		}
		definition.Expression = expression
		definition.OID = 0
		replacement.ColumnMeta.Defaults[attribute] = definition
	}
	if err := h.CaptureCheckSnapshots(&replacement, false); err != nil {
		return fail(err)
	}
	return &replacement, transformation, nil
}
