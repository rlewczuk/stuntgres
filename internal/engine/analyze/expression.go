package analyze

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ExpressionContext uint8

const (
	ProjectionContext ExpressionContext = iota
	FilterContext
	AssignmentContext
	FunctionArgumentContext
)

type ExpressionPolicy struct {
	Context          ExpressionContext
	Expected         sqltype.Usage
	Mode             sqltype.CoercionContext
	Column           string
	booleanConstruct string
	// Selected textual function roles admit internal CHAR annotations.
	stringInput bool
	// Only width_bucket's threshold argument consumes existing enum arrays.
	// This is not permission for generic casts, storage or domain declarations.
	thresholdArrayInput bool
	// Selected string INSERT rows analyze occurrences before target assignment.
	deferAssignment bool
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
}

// Binder binds the expressions of one statement. Host is the engine seam
// (12c); Ctx, Expected and Input are the statement inputs; Dependencies is
// the statement's dependency manifest. Everything else lives in the value
// groups of binder_groups.go.
type Binder struct {
	Host         Host
	Ctx          context.Context
	Expected     Expected
	Input        sqltype.InputEnvironment
	Dependencies []catalog.ExpressionDependency
	// Identity of the actual missing-call diagnostic, not a SQLSTATE heuristic.
	unresolvedCallable error

	Target   targetContext
	Params   parameterState
	Consumer ConsumerContext
	Capture  ViewCapture
	Stored   StoredSyntaxState
	Prepared PreparedCaptures
	Clause   ClauseState
	Grouping GroupingState
	Query    QueryScopeState
	cte      cteState
}

func NewBinder(ctx context.Context, host Host, input sqltype.InputEnvironment, expected Expected) *Binder {
	b := &Binder{Host: host, Ctx: ctx, Params: parameterState{Parameters: make(map[int]sqltype.Usage)}, Input: input, Expected: expected}
	host.CaptureEnumOperationSafety(&b.Input)
	if expected.Held {
		b.SeedParameters(expected.Parameters.Types)
		if expected.RefreshTemporals {
			for _, capture := range expected.Temporals {
				if !RefreshableTemporal(capture) {
					b.Prepared.Temporals = append(b.Prepared.Temporals, capture)
				}
			}
		}
	}
	return b
}

func (b *Binder) SeedParameters(types []sqltype.Usage) {
	b.Params.formalParameters = types
	currentType := RecordTypeMapper(func(t *sqltype.Descriptor) *sqltype.Descriptor { return b.Type(t.ID()) })
	for i, usage := range types {
		if usage.Type != nil {
			usage.Type = currentType(usage.Type)
		}
		b.Params.Parameters[i+1] = usage
	}
}

// Binding must retain the caller's catalog/path authority, including a private
// CREATE candidate, read through the operation's definition reader. Execution
// still opens providers through live authorization.
func (b *Binder) BindingAuthorization() (Authority, error) {
	return b.Host.BindingAuthority(b.Query.Auth)
}
func (b *Binder) Type(id sqltype.ID) *sqltype.Descriptor {
	d, _ := b.definitionType(id)
	return d
}
func (b *Binder) dependency(ref ids.ObjectRef, revision ids.DefinitionRevision) {
	for _, d := range b.Dependencies {
		if d.Ref == ref {
			return
		}
	}
	b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{ref, revision})
}
func (b *Binder) BindingCatalog() catalog.Read {
	if b.Query.Auth != nil {
		return b.Query.Auth.ReadCatalog()
	}
	return b.Host.CurrentCatalog()
}

func (b *Binder) Info(u sqltype.Usage, pos parser.Position, p expr.Properties) expr.Info {
	view := b.BindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
	revision := ids.DefinitionRevision(1)
	if descriptor, ok := b.definitionDescriptor(address); ok {
		revision = descriptor.Revision()
	}
	b.dependency(b.Host.CatalogOwner().Domain.Ref(view.Epoch(), address), revision)
	if u.Collation.ID != 0 {
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}
		if d, ok := b.definitionDescriptor(address); ok {
			b.dependency(b.Host.CatalogOwner().Domain.Ref(view.Epoch(), address), d.Revision())
		}
	}
	return expr.Info{Usage: u, Pos: pos, Property: p}
}
func (b *Binder) Constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 || b.Params.ClosedRoutineFormals && n > len(b.Params.formalParameters) {
		return expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.Params.Parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.Params.UpdateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.CoerceParameter(plan.ParameterExpr{Info: expr.Info{Pos: pos}, Number: n}, u)
			return err
		}
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.Params.Parameters[n] = u.Type.Usage()
	return nil
}
func (b *Binder) Finish() (ParameterInfo, error) {
	// Retained unknown ANY and null-test occurrences share binding order.
	// Diagnose occurrence mismatches before gaps or unresolved shared slots.
	for _, p := range b.Params.AnyParameters {
		if b.Params.Parameters[p.Number].Type != nil {
			return ParameterInfo{}, expr.ErrorAt("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.Number), p.Pos)
		}
	}
	keys := make([]int, 0, len(b.Params.Parameters))
	for n := range b.Params.Parameters {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for i, n := range keys {
		if n != i+1 || b.Params.Parameters[n].Type == nil {
			return ParameterInfo{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	info := ParameterInfo{Count: len(keys), Types: make([]sqltype.Usage, len(keys))}
	for i, n := range keys {
		info.Types[i] = b.Params.Parameters[n]
		if err := b.CaptureTypeDiagnosticInput(info.Types[i].Type); err != nil {
			return ParameterInfo{}, err
		}
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *Binder) FinalizeProjection(node expr.Expr) (expr.Expr, error) {
	p, ok := node.(plan.ParameterExpr)
	if !ok || p.Usage.Type != nil {
		return node, nil
	}
	return b.CoerceParameter(p, b.Type(sqltype.Text).Usage())
}
func (b *Binder) Column(raw parser.ColumnExpr) (node expr.Expr, err error) {
	defer func() {
		if err == nil || !b.Params.ClosedRoutineFormals {
			return
		}
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "42703" && diagnostic.Code != "42P01" {
			return
		}
		parts := raw.Name.Parts
		if len(parts) != 1 && (len(parts) != 2 || parts[0].Text != b.Params.RoutineName) {
			return
		}
		for i, name := range b.Params.RoutineNames {
			if name != "" && name == parts[len(parts)-1].Text {
				node, err = b.Bind(parser.ParameterExpr{Number: i + 1, Pos: raw.Pos}, ExpressionPolicy{})
				if err == nil && b.Consumer.frozenDefinition {
					if b.Stored.Syntax == nil {
						b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
					}
					b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = parser.ParameterExpr{Number: i + 1, Pos: raw.Pos}
				}
				return
			}
		}
	}()
	if b.Clause.DomainValueUsage != nil && len(raw.Name.Parts) == 1 && raw.Name.Parts[0].Text == "value" {
		return plan.DomainValueExpr{Info: b.Info(*b.Clause.DomainValueUsage, raw.Pos, expr.Properties{DomainDependent: true})}, nil
	}
	if b.Target.GeneratedOwner != nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		owner := b.Target.GeneratedOwner
		qualified := len(raw.Name.Parts) == 1 || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == owner.Name() || len(raw.Name.Parts) == 3 && raw.Name.Parts[1].Text == owner.Name() && raw.Name.Parts[0].Text == b.Query.Namespace.Qualifiers[0].Schema
		if qualified {
			if name == "tableoid" {
				ref := b.Query.Sources[0].Ref
				return plan.TableOIDExpr{Info: b.Info(b.Type(sqltype.Oid).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Owner: ref}, nil
			}
			if catalog.IsSystemColumn(name) {
				return nil, expr.ErrorAt("42P10", fmt.Sprintf(`cannot use system column "%s" in column generation expression`, name), raw.Pos)
			}
		}
		node, err := b.queryColumn(raw)
		if err != nil {
			// A bare relation name is a whole-row reference only after ordinary
			// column lookup has failed; a same-named attribute takes precedence.
			if name == owner.Name() && len(raw.Name.Parts) == 1 {
				diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "This would cause the generated column to depend on its own value."
				return nil, diagnostic
			}
			return nil, err
		}
		if _, whole := node.(plan.RowExpr); whole {
			diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
			diagnostic.Detail = "This would cause the generated column to depend on its own value."
			return nil, diagnostic
		}
		if column, ok := node.(plan.ColumnExpr); ok {
			if _, generated := owner.ColumnMeta.Generations[column.Origin.Address.SubID]; generated {
				diagnostic := expr.ErrorAt("42P17", fmt.Sprintf(`cannot use generated column "%s" in column generation expression`, name), raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "A generated column cannot reference another generated column."
				return nil, diagnostic
			}
		}
		return node, nil
	}
	if b.Consumer.CheckConsumer && b.Clause.DomainValueUsage == nil && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		switch name {
		case "ctid", "xmin", "cmin", "xmax", "cmax":
			return nil, expr.ErrorAt("42P10", `system column "`+name+`" reference in check constraint is invalid`, raw.Pos)
		}
	}
	if b.Target.returningAliases != nil {
		return b.returningColumn(raw)
	}
	if b.Query.Namespace != nil {
		return b.queryColumn(raw)
	}
	if len(raw.Name.Parts) == 2 && !b.Target.deleteTarget {
		return nil, expr.ErrorAt("0A000", "qualified column references are unsupported", raw.Pos)
	}
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.Target.Relation == nil {
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	if len(raw.Name.Parts) == 2 {
		qualifier := raw.Name.Parts[0].Text
		visible := b.Target.Name
		if b.Target.alias != "" {
			visible = b.Target.alias
		}
		if qualifier != visible {
			if qualifier == b.Target.Name && b.Target.alias != "" {
				err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, qualifier), raw.Pos).(*sqlstate.Error)
				err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.Target.alias)
				return nil, err
			}
			return nil, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, qualifier), raw.Pos)
		}
	}
	i, ok := b.Target.Relation.Index[name]
	if !ok {
		if len(raw.Name.Parts) == 2 {
			return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", raw.Name.Parts[0].Text, name), raw.Pos)
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.columnAt(i, raw.Pos), nil
}
func (b *Binder) useColumn(i int) ids.ObjectRef {
	return b.useColumnOf(b.Target.Relation, i)
}

func (b *Binder) useColumnOf(relation *catalog.Relation, i int) ids.ObjectRef {
	ref := b.Target.Ref
	ref.Address.SubID = relation.Attributes[i]
	b.dependency(ref, relation.Revision())
	used := false
	for _, slot := range b.Target.Uses {
		if slot == i {
			used = true
			break
		}
	}
	if !used {
		b.Target.Uses = append(b.Target.Uses, i)
	}
	b.Info(relation.Columns[i].Usage, parser.Position{Offset: -1}, expr.Properties{})
	return ref
}
func (b *Binder) columnAt(i int, pos parser.Position) expr.Expr {
	return b.columnAtOf(b.Target.Relation, i, pos)
}

func (b *Binder) columnAtOf(relation *catalog.Relation, i int, pos parser.Position) expr.Expr {
	ref := b.useColumnOf(relation, i)
	column := plan.ColumnExpr{Info: expr.Info{Usage: relation.Columns[i].Usage, Pos: pos, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: i}
	if b.Target.GeneratedOwner == nil {
		column = column.WithGeneration(relation, i, 0)
	}
	return column
}
func (b *Binder) Bind(raw parser.Expr, policy ExpressionPolicy) (boundResult expr.Expr, bindError error) {
	if policy.Context == FilterContext && !policy.operand && policy.booleanConstruct == "" {
		return b.BindBoolean(raw, policy.Context, "WHERE")
	}
	if err := b.Ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.Clause.SelectedOperatorArgs) {
		node := b.Clause.SelectedOperatorArgs[-slot.Number-1]
		if b.Grouping.ArgumentOwner.Active && !b.Grouping.ArgumentOwner.Found {
			// Selection analyzed this operand before the winning argument
			// owner existed. Preserve its lexical occurrence even if the
			// signature does not retain an executable argument.
			if nested := WindowOccurrence(node); nested != nil && nested.Frame == b.FrameID() {
				b.Grouping.ArgumentOwner.First, b.Grouping.ArgumentOwner.Found = nested.Pos, true
			}
		}
		return node, nil
	}
	defer func() {
		if bindError != nil || boundResult == nil {
			return
		}
		if policy.Context == AssignmentContext && !policy.deferAssignment {
			bindError = rejectRegexpSets(boundResult, "UPDATE", false)
		}
		if policy.Context == FilterContext {
			clause := b.Clause.AggregateClause
			if clause == "" || clause == "SELECT" {
				clause = "WHERE"
			}
			bindError = rejectRegexpSets(boundResult, clause, false)
		}
		if bindError != nil {
			boundResult = nil
		}
	}()
	var node expr.Expr
	if selected, handled, err := b.BindSQLRoutineSyntax(raw, policy); handled {
		if err != nil {
			return nil, err
		}
		node = selected
	}
	if node == nil && b.Consumer.PartitionConsumer != "" {
		switch r := raw.(type) {
		case parser.ColumnExpr:
			if b.Consumer.PartitionConsumer == "partition bound" {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in partition bound expression", r.Pos)
			}
		case parser.IndexSubqueryExpr:
			return nil, b.Consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.Consumer.PartitionConsumer, r.Pos)
		case parser.SubqueryExpr:
			return nil, b.Consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.Consumer.PartitionConsumer, r.Pos)
		case parser.AggregateExpr:
			if r.Window == nil && r.Name != "row_number" && r.Name != "ntile" && !functions.WindowValueName(r.Name) && !functions.WindowOffsetName(r.Name) {
				for _, arg := range r.Args {
					if _, err := b.Bind(arg, ExpressionPolicy{Context: ProjectionContext}); err != nil {
						return nil, err
					}
				}
				return nil, b.Consumer.partitionExpressionError("42803", "aggregate functions are not allowed in "+b.Consumer.partitionContextPlural(), r.Pos)
			}
		}
	}
	if node == nil && b.Clause.FunctionProjection.matches(raw) {
		node = b.Clause.FunctionProjection.Operand
	}
	if node == nil {
		switch r := raw.(type) {
		case parser.MergeActionExpr:
			var err error
			node, err = b.bindMergeAction(r)
			if err != nil {
				return nil, err
			}
		case parser.IndexSubqueryExpr:
			if b.Consumer.IndexPredicateConsumer {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", r.Pos)
			}
			return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", r.Pos)
		case parser.SubqueryExpr:
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in EXECUTE parameter", r.Pos)
			}
			var err error
			node, err = b.bindSubquery(r)
			if err != nil {
				return nil, err
			}
		case parser.RowExpr:
			var err error
			node, err = b.BindRow(r, policy.Expected, policy.Mode, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.FieldExpr:
			var err error
			node, err = b.bindField(r)
			if err != nil {
				return nil, err
			}
		case parser.StarExpr:
			var err error
			node, err = b.wholeRow(r.Qualifier, r.Pos, true)
			if err != nil {
				return nil, err
			}
		case parser.IdentityExpr:
			var err error
			node, err = functions.BindIdentity(b, r.Name, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.CurrentDateTimeExpr:
			var err error
			var warning *sqlstate.Error
			node, warning, err = functions.BindCurrentDateTime(b, r)
			if err != nil {
				return nil, err
			}
			// A held description already analyzed this syntax. Execute must
			// still bind fresh descriptors and evaluate fresh values.
			if warning != nil && !b.Expected.Held {
				b.Host.EmitWarning(*warning)
			}
		case parser.ForbiddenGenerationExpr:
			return nil, expr.ErrorAt(r.Code, r.Message, r.Pos)
		case parser.GroupingExpr:
			var err error
			node, err = b.bindGrouping(r)
			if err != nil {
				return nil, err
			}
		case parser.AggregateExpr:
			ownedAggregate := b.Clause.selectedAggregate
			b.Clause.selectedAggregate = nil
			if r.Window == nil && b.cte.recursiveFrames[b.FrameID()] {
				return nil, expr.ErrorAt("42P19", "aggregate functions are not allowed in a recursive query's recursive term", r.Pos)
			}
			if b.Params.ExecuteParameters {
				if r.Window != nil {
					return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
				}
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in EXECUTE parameters", r.Pos)
			}
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
			if r.Schema != nil {
				name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
			}
			diagnosticName := name
			if len(b.Clause.selectedWindowCallee.Parts) != 0 {
				// Consume before Signature binds any children. Nested calls
				// must retain their own lexical diagnostic spelling.
				diagnosticName = b.Clause.selectedWindowCallee
				b.Clause.selectedWindowCallee = parser.ObjectName{}
			}
			parentAggregates, parentWindows := b.Grouping.Aggregates, b.Grouping.WindowCalls
			defer func() {
				if bindError != nil {
					b.Grouping.Aggregates, b.Grouping.WindowCalls = parentAggregates, parentWindows
				}
			}()
			if functions.HypotheticalSetName(r.Name) && !r.Within && len(r.Args) == 0 && r.Window != nil {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			if r.Within && !functions.OrderedSetName(r.Name) {
				return b.rejectOrdinaryWithin(r)
			}
			if ownedAggregate != nil && r.Window != nil {
				return nil, expr.ErrorAt("0A000", "user-defined aggregate window execution is unsupported", r.Pos)
			}
			if r.Window != nil && !functions.OrderedSetName(r.Name) {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			// A window function without OVER resolves its signature first.
			if function, ok := functions.LookupWindowFunction(r.Name); ok && (!functions.HypotheticalSetName(r.Name) || !r.Within && len(r.Args) == 0) {
				if _, err := function.Signature(b, r, name); err != nil {
					return nil, err
				}
				return nil, expr.ErrorAt("42809", "window function "+catalog.ObjectNameText(diagnosticName)+" requires an OVER clause", r.Pos)
			}
			parentOwner := b.Grouping.ArgumentOwner
			b.Grouping.ArgumentOwner = ArgumentWindowOwner{Active: true}
			defer func() { b.Grouping.ArgumentOwner = parentOwner }()
			if b.Target.GeneratedOwner != nil {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in column generation expressions", r.Pos)
			}
			if b.Consumer.IndexPredicateConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index predicates", r.Pos)
			}
			if b.Consumer.IndexConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index expressions", r.Pos)
			}
			if b.Clause.AggregateClause == "VALUES" || b.Clause.AggregateClause == "INSERT VALUES" && (r.Name == "string_agg" || r.Name == "array_agg") {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in VALUES", r.Pos)
			}
			if b.Consumer.CheckConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in check constraints", r.Pos)
			}
			if b.Consumer.RoutineDefault {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in DEFAULT expressions", r.Pos)
			}
			var err error
			var returning sqltype.Usage
			if ownedAggregate != nil {
				node, err = b.bindOwnedAggregate(r, ownedAggregate, diagnosticName)
			} else if definition, ok := functions.LookupAggregate(r.Name); ok {
				node, returning, err = b.BindRegisteredAggregate(r, name, definition)
			} else if r.Name == "int_array_aggregate" {
				node, err = b.bindIntaggAggregate(r, name)
			} else {
				// Every other name binds as count, for its 42883 diagnostics.
				node, err = functions.BindCount(b, r, name)
			}
			if err != nil {
				return nil, err
			}
			node, err = b.ownAggregate(node)
			if err != nil {
				return nil, err
			}
			if returning.Type != nil {
				node, err = b.aggregateReturning(r, node, returning)
				if err != nil {
					return nil, err
				}
			}
		case parser.CaseExpr:
			var err error
			node, err = b.bindCase(r)
			if err != nil {
				return nil, err
			}
		case parser.ConditionalExpr:
			var err error
			node, err = b.bindConditional(r)
			if err != nil {
				return nil, err
			}
		case parser.SQLJSONQueryExpr:
			var err error
			node, err = functions.BindSQLJSONQuery(b, r)
			if err != nil {
				return nil, err
			}
		case parser.SQLJSONExpr:
			var err error
			node, err = b.bindSQLJSON(r)
			if err != nil {
				return nil, err
			}
		case parser.JSONPredicateExpr:
			var err error
			node, err = functions.BindJSONPredicate(b, r)
			if err != nil {
				return nil, err
			}
		case parser.FunctionExpr:
			if b.Params.ExecuteParameters && r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
			}
			var err error
			node, err = b.BindFunction(r)
			if err != nil {
				return nil, err
			}
			switch inquiry := node.(type) {
			case plan.ExtensionPathsExpr:
				b.Consumer.CatalogSnapshot = true
			case expr.SnapshotDemander:
				demand, _, _ := inquiry.SnapshotDemand(plan.NullLiteral)
				b.Consumer.CatalogSnapshot = b.Consumer.CatalogSnapshot || demand
			}
			if r.Window != nil {
				return nil, expr.ErrorAt("42809", "OVER specified, but "+catalog.ObjectNameText(r.Name)+" is not a window function nor an aggregate function", r.Pos)
			}
		case parser.CallExpr:
			var err error
			node, err = functions.BindSequenceCall(b, r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayExpr:
			var err error
			var target sqltype.Usage
			if policy.Mode == sqltype.Explicit {
				target = policy.Expected
			}
			node, err = b.BindArray(r, target)
			if err != nil {
				return nil, err
			}
		case parser.SubscriptExpr:
			var err error
			node, err = b.BindSubscript(r)
			if err != nil {
				return nil, err
			}
		case parser.RegclassExpr:
			var err error
			node, err = b.bindRegclassCast(r.Child, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.ColumnExpr:
			if pseudo, matched, err := b.RuleColumn(r); matched {
				if err != nil {
					return nil, err
				}
				node = pseudo
				break
			}
			if b.Consumer.DefaultConsumer {
				if b.Consumer.RoutineDefault {
					return nil, expr.ErrorAt("42703", fmt.Sprintf("column %q does not exist", catalog.ObjectNameText(r.Name)), r.Pos)
				}
				return nil, expr.ErrorAt("0A000", "cannot use column reference in DEFAULT expression", r.Pos)
			}
			if policy.Context == AssignmentContext && !b.Params.ClosedRoutineFormals {
				return nil, expr.ErrorAt("0A000", "column references outside SELECT are unsupported", r.Pos)
			}
			var err error
			node, err = b.Column(r)
			if err != nil {
				return nil, err
			}
		case parser.ParameterExpr:
			if b.Stored.CheckLiterals != nil {
				if r.Number <= 0 || r.Number > len(b.Stored.CheckLiterals) {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
				}
				node = b.Stored.CheckLiterals[r.Number-1]
				break
			}
			if b.Params.NoParameters || b.Consumer.DefaultConsumer || r.Number <= 0 || b.Params.ClosedRoutineFormals && r.Number > len(b.Params.formalParameters) {
				return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
			}
			u := policy.Expected
			if established := b.Params.updateInputParameters[r.Number]; established.Type != nil {
				u = established
			}
			retained := r.Number <= len(b.Params.formalParameters) && b.Params.formalParameters[r.Number-1].Type != nil
			if retained {
				u = b.Params.formalParameters[r.Number-1]
			}
			// Retained parameter contracts keep their logical input type, but their
			// codec must come from the admitted catalog, including private generations.
			if b.Expected.Held && r.Number <= len(b.Expected.Parameters.Types) {
				u = b.Expected.Parameters.Types[r.Number-1]
				retained = u.Type != nil
			}
			if retained {
				catalog := b.BindingCatalog()
				if b.Expected.Held && StatementCatalogType(u.Type) && b.Expected.CatalogOwner != nil &&
					(b.Expected.CatalogOwner != catalog.Owner() || b.Expected.CatalogEpoch != catalog.Epoch()) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				current, _ := catalog.LookupType(u.Type.ID())
				if u.Type.IsDeferredRecord() || u.Type.ID() == 2249 && u.Type.IsComposite() {
					// Anonymous shapes and deferred RECORD are formal occurrence
					// authorities, never catalog's lookup-only RECORD identity.
					current = b.Params.Parameters[r.Number].Type
				}
				if current == nil && StatementCatalogType(u.Type) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				u.Type = current
				b.Params.Parameters[r.Number] = u
			}
			if u.Type == nil {
				u = b.Params.Parameters[r.Number]
			}
			if u.Type == nil {
				b.Params.Parameters[r.Number] = sqltype.Usage{}
				node = plan.ParameterExpr{Info: expr.Info{Pos: r.Pos, Property: expr.Properties{ParameterDependent: true}}, Number: r.Number}
			} else {
				if err := b.Constrain(r.Number, u, r.Pos); err != nil {
					return nil, err
				}
				info := b.Info(u.Type.Usage(), r.Pos, expr.Properties{ParameterDependent: true})
				if b.Params.ClosedRoutineFormals && b.Params.routineInputCollation.ID != 0 && info.Usage.Collation.ID != 0 {
					// Function parameters inherit the effective input collation
					// implicitly; the caller's COLLATE origin never crosses here.
					info.Usage.Collation = b.Params.routineInputCollation
				}
				node = plan.ParameterExpr{Info: info, Number: r.Number}
			}
		case parser.LiteralExpr:
			v := r.Value
			u := policy.Expected
			var value sqltype.Value
			var err error
			switch v.Kind {
			case parser.NullValue:
				if u.Type == nil {
					u = b.Type(sqltype.Text).Usage()
				}
				if u.Type.IsDomain() {
					// Fresh NULL is an unconstrained physical input. The outer
					// assignment owns inherited typmods and domain constraints.
					u = u.BaseUsage().Type.Usage()
				}
				value = sqltype.Null(u.Type)
			case parser.BitStringValue:
				value, err = b.InputConstant(v.Text, b.Type(sqltype.Bit).Usage(), v.Pos)
			case parser.StringValue:
				if u.Type == nil {
					u = b.Type(sqltype.Text).Usage()
				}
				inputUsage := u.Type.Usage()
				if u.Type.ID() == sqltype.Interval {
					inputUsage = u
				}
				if u.Type.IsDomain() {
					inputUsage = u.BaseUsage()
					if inputUsage.Type.ID() != sqltype.Interval {
						inputUsage = inputUsage.Type.Usage()
					}
				}
				value, err = b.InputConstant(v.Text, inputUsage, v.Pos)
			case parser.NumericValue:
				value, err = b.InputConstant(v.Text, b.Type(sqltype.Numeric).Usage(), v.Pos)
			case parser.IntegerValue:
				id := sqltype.Int4
				if v.Integer < -2147483648 || v.Integer > 2147483647 {
					id = sqltype.Int8
				}
				value, err = sqltype.Integer(b.Type(id), v.Integer)
			case parser.BooleanValue:
				value = sqltype.Boolean(b.Type(sqltype.Bool), v.Boolean)
			default:
				return nil, fmt.Errorf("stuntgres: invalid scalar literal")
			}
			if err != nil {
				return nil, literalInputError(err, r)
			}
			literalUsage := value.Type().Usage()
			if v.Kind == parser.StringValue && u.Type.BaseType().ID() == sqltype.Interval {
				literalUsage = u.BaseUsage()
			}
			info := b.Info(literalUsage, v.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: info, Value: value}
			if literalUsage.Type.ID() == sqltype.Regclass && !value.IsNull() {
				text, _ := value.Text()
				oid, err := strconv.ParseUint(text, 10, 32)
				if err != nil {
					return nil, err
				}
				auth, err := b.BindingAuthorization()
				if err != nil {
					return nil, err
				}
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
				if descriptor, exists := auth.ReadCatalog().Descriptor(address); exists {
					b.dependency(b.Host.CatalogOwner().Domain.Ref(auth.ReadCatalog().Epoch(), address), descriptor.Revision())
				}
			}
		case parser.TypedLiteralExpr:
			rawType := r.TypeName
			saved, found, err := b.CaptureAt(r.Pos.Offset)
			if err != nil {
				return nil, err
			}
			if found {
				rawType.Modifiers = nil
			}
			u, err := b.Host.ResolveType(rawType)
			if err != nil {
				return nil, err
			}
			if r.RequiresDomain && !u.Type.IsDomain() {
				return nil, expr.ErrorAt("0A000", "typed literals are unsupported", rawType.Pos)
			}
			if found {
				u.Typmod = saved.Typmod
				if saved.Typed {
					u.Typmod = saved.TypedTypmod
				}
			}
			inputUsage := u.BaseUsage()
			if inputUsage.Type.ID() != sqltype.Interval {
				inputUsage = inputUsage.Type.Usage()
			}
			value, err := b.InputConstant(r.Text, inputUsage, r.Pos)
			if err != nil {
				return nil, InputErrorAt(err, r.Pos)
			}
			if b.collectTemporalConstants() {
				for i := range b.Prepared.Temporals {
					if b.Prepared.Temporals[i].Offset == r.Pos.Offset && b.Prepared.Temporals[i].TypeID == u.Type.ID() {
						b.Prepared.Temporals[i].Typed = true
						b.Prepared.Temporals[i].TypedTypmod = u.Typmod
					}
				}
			}
			literalInfo := b.Info(inputUsage, r.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: literalInfo, Value: value}
			if inputUsage != u {
				coercionPlan, err := sqltype.PlanCoercion(inputUsage, u, sqltype.Explicit)
				if err != nil {
					return nil, err
				}
				info, err := b.coercionInfo(b.Info(u, r.Pos, CoercionProperties(node, coercionPlan)), node)
				if err != nil {
					return nil, err
				}
				node = plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Explicit}
			}
		case parser.CollateExpr:
			child, err := b.Bind(r.Child, policy)
			if err != nil {
				return nil, err
			}
			name := r.Name
			name.Parts = append([]parser.Identifier(nil), name.Parts...)
			name.Parts[0].Pos = r.Pos
			usage, err := b.Host.ResolveColumnCollation(child.ResultUsage(), &name)
			if err != nil {
				return nil, err
			}
			info := b.Info(usage, r.Pos, child.Properties())
			info.CollationExplicit = r.Pos
			node = plan.CollationExpr{Info: info, Child: child}
		case parser.CastExpr:
			var err error
			node, err = b.Cast(r, policy)
			if err != nil {
				return nil, err
			}
		case parser.TimeZoneExpr:
			var err error
			node, err = functions.BindTimeZone(b, r)
			if err != nil {
				return nil, err
			}
		case parser.LogicalExpr:
			var err error
			node, err = b.bindLogical(r, policy.Context)
			if err != nil {
				return nil, err
			}
		case parser.PredicateExpr:
			if policy.Context == AssignmentContext {
				return nil, fmt.Errorf("stuntgres: predicate in assignment scalar")
			}
			var err error
			node, err = b.bindPredicate(r, policy.Context)
			if err != nil {
				return nil, err
			}
		case parser.InListExpr:
			var err error
			node, err = b.bindInList(r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayComparisonExpr:
			var err error
			node, err = b.bindArrayComparison(r)
			if err != nil {
				return nil, err
			}
		case parser.OperatorExpr:
			var err error
			node, err = b.BindCatalogOperator(r, policy)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("stuntgres: unsupported scalar node")
		}
	}
	if b.Clause.FunctionProjection.matches(raw) && b.Clause.FunctionProjection.Operand == nil && node.ResultUsage().Type != nil && node.ResultUsage().Type.Concrete() && node.ResultUsage().Type.BaseType().IsComposite() {
		b.Clause.FunctionProjection.Operand = node
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.IsDomain() && node.ResultUsage().Type.BaseType().ID() == sqltype.Bool {
		var err error
		node, err = b.CoerceConditional(ComparisonOperand{Node: node}, b.Type(sqltype.Bool).Usage(), policy.booleanConstruct)
		if err != nil {
			return nil, err
		}
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.ID() != sqltype.Bool {
		return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", policy.booleanConstruct, node.ResultUsage().Type.DiagnosticName()), parser.ExpressionStart(raw))
	}
	// Logical children already passed this policy independently. Do not walk
	// every prefix again when binding a long AND/OR chain.
	if _, logical := node.(plan.LogicalExpr); policy.booleanConstruct != "" && !logical {
		returnsSet := false
		plan.WalkExpression(node, func(child expr.Expr) {
			returnsSet = returnsSet || exec.SourceReturnsSet(child)
		})
		if returnsSet {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must not return a set", policy.booleanConstruct), parser.ExpressionStart(raw))
		}
	}
	if !policy.deferAssignment && (policy.Context == AssignmentContext || policy.Context == FunctionArgumentContext) && node.ResultUsage() != policy.Expected {
		if err := b.CaptureTypeDiagnosticInput(policy.Expected.Type); err != nil {
			return nil, err
		}
		if policy.Expected.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		input, annotations := DomainCoercionInput(node, policy.Expected)
		info := b.Info(policy.Expected, raw.SourcePosition(), input.Properties())
		var err error
		if policy.Context == AssignmentContext {
			info, err = StorageCoercionInfo(b.BindingCatalog(), info, input)
		} else {
			info, err = b.coercionInfo(info, input)
		}
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, policy.Mode)
		if err != nil {
			// Retain the resolved source for the assignment caller's diagnostic.
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && policy.Column != "" {
				copy := *d
				copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", policy.Column, b.DiagnosticTypeName(policy.Expected.Type), b.DiagnosticTypeName(node.ResultUsage().Type))
				copy.Hint = "You will need to rewrite or cast the expression."
				copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
				return node, &copy
			}
			return node, err
		}
		if coercionPlan.RequiresCatalogOutput() {
			b.Prepared.RegclassConcat = true
		}
		info.Property = CoercionProperties(input, coercionPlan)
		if folded, ok, err := b.foldRegtypeConstant(input, coercionPlan, info); ok || err != nil {
			if err != nil {
				return nil, err
			}
			node = RestoreDomainCastCollations(annotations, folded)
		} else {
			node = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: policy.Mode})
		}
		if policy.Context == AssignmentContext {
			var err error
			node, err = StorageDestination(node, policy.Expected, raw.SourcePosition())
			if err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

func functionCastError(raw parser.CastExpr, source string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", functions.SignatureHint
	if ambiguous {
		code, ending = "42725", "is not unique"
		hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	name := raw.Callee
	if raw.TypeName.Schema != nil {
		name = raw.TypeName.Schema.Text + "." + name
	}
	d := expr.ErrorAt(code, fmt.Sprintf("function %s(%s) %s", name, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *Binder) Cast(raw parser.CastExpr, outerPolicy ExpressionPolicy) (expr.Expr, error) {
	if raw.Syntax == parser.FunctionCast && legacyFunctionCastPredicate(raw.Child) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if !b.Consumer.DefaultConsumer && b.Query.Namespace == nil && b.Target.returningAliases == nil && b.Target.RuleNamespace == nil {
		if err := qualifiedCastChild(raw.Child); err != nil {
			return nil, err
		}
	}
	visible := true
	if raw.Syntax == parser.FunctionCast {
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: raw.Callee, Pos: raw.Pos}}}
		if raw.TypeName.Schema != nil {
			name.Parts = []parser.Identifier{*raw.TypeName.Schema, {Text: raw.Callee, Pos: raw.Pos}}
		}
		var err error
		visible, err = b.builtinVisible(name, FunctionLookup)
		if err != nil {
			return nil, err
		}
	}
	if raw.Syntax == parser.FunctionCast && (!visible || raw.Callee != "oid" && raw.Callee != "xid" && raw.Callee != "cid" && raw.Callee != "xid8" && raw.Callee != "int4" && raw.Callee != "int8" && raw.Callee != "text" && raw.Callee != "bool") {
		source := "unknown"
		if literal, ok := raw.Child.(parser.LiteralExpr); !ok || (literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue) {
			child, err := b.Bind(raw.Child, ExpressionPolicy{Context: ProjectionContext})
			if err != nil {
				return nil, err
			}
			if child.ResultUsage().Type != nil {
				source = child.ResultUsage().Type.DiagnosticName()
			}
		}
		return nil, functionCastError(raw, source, false)
	}
	rawNull, isNull := raw.Child.(parser.LiteralExpr)
	unknownNull := raw.Syntax != parser.FunctionCast && isNull && rawNull.Value.Kind == parser.NullValue
	var target sqltype.Usage
	var err error
	annotation, err := b.DomainCastAnnotation(raw)
	if err != nil {
		return nil, err
	}
	if b.Clause.JSONBRecordBase {
		annotation, err = b.JSONBRecordCastAnnotation(raw)
		if err != nil {
			return nil, err
		}
	}
	if outerPolicy.thresholdArrayInput {
		target, err = b.Host.ResolveAnnotationTypeInput(annotation, unknownNull, false, false, false, true)
	} else if outerPolicy.stringInput {
		target, err = b.Host.ResolveStringAnnotationType(annotation, unknownNull)
	} else {
		target, err = b.Host.ResolveAnnotationType(annotation, unknownNull)
	}
	if err != nil {
		return nil, err
	}
	if b.Clause.JSONBRecordBase && raw.Syntax != parser.FunctionCast && target.Type.IsUserDefined() && target.Type.BaseType().IsComposite() {
		b.Prepared.CaptureComposite(PreparedTypeKey{Offset: raw.TypeName.Pos.Offset, Kind: CompositeRecordArgument}, target, nil)
	}
	if (target.Type.IsDomain() || target.Type.IsArray() && target.Type.Element().IsDomain()) && raw.Syntax != parser.FunctionCast {
		if b.Prepared.Types == nil {
			b.Prepared.Types = make(map[PreparedTypeKey]TypeOccurrence)
		}
		key := PreparedTypeKey{Offset: raw.TypeName.Pos.Offset, Kind: DomainCast}
		b.Prepared.Types[key] = TypeOccurrence{Kind: DomainCast, Base: target}
	}
	if target.Type.ID() == sqltype.Unknown {
		// UNKNOWN is syntax provenance only, never an executable datum type.
		return b.Bind(raw.Child, outerPolicy)
	}
	if row, ok := raw.Child.(parser.RowExpr); ok && target.Type.BaseType().IsComposite() {
		return b.BindRow(row, target, sqltype.Explicit, raw.Pos)
	}
	if target.Type.ID() == sqltype.Regclass {
		return b.bindRegclassCast(raw.Child, raw.Pos)
	}
	unsupportedTarget := !target.Type.IsUserDefined() && !target.Type.IsDomain() && !target.Type.IsDeferredRecord() && !sqltype.StoredScalarCastType(target.Type.ID()) && !(outerPolicy.stringInput && target.Type.ID() == sqltype.Char) && target.Type.ID() != sqltype.PgNodeTree && !target.Type.IsArray()
	if unsupportedTarget && !b.Consumer.DatetimeZoneOperands {
		return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	if target.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	policy := ExpressionPolicy{Context: ProjectionContext, thresholdArrayInput: outerPolicy.thresholdArrayInput}
	switch child := raw.Child.(type) {
	case parser.LiteralExpr:
		if child.Value.Kind == parser.StringValue || child.Value.Kind == parser.NullValue {
			policy.Expected = target
			if target.Type.IsDomain() {
				policy.Expected = target.Type.BaseType().Usage()
				if policy.Expected.Type.ID() == sqltype.Interval {
					policy.Expected = target.BaseUsage()
				}
			}
		}
	case parser.ArrayExpr:
		policy.Expected = target
		policy.Mode = sqltype.Explicit
	case parser.CastExpr:
		if unknownNullSyntax(child) {
			policy.Expected = target
		}
	case parser.ParameterExpr:
		if b.Stored.CheckLiterals != nil {
			break // bind resolves the captured literal without parameter inference.
		}
		if b.Params.NoParameters || b.Consumer.DefaultConsumer {
			return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.Params.Parameters[child.Number].Type == nil {
			if raw.Syntax == parser.FunctionCast && target.Type.ID() != sqltype.Text {
				if child.Number <= 0 {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
				}
				return nil, functionCastError(raw, "unknown", true)
			}
			policy.Expected = target
		}
	}
	child, err := b.Bind(raw.Child, policy)
	if err != nil {
		return nil, err
	}
	// Bind annotations before moving them so their original type checks,
	// locations, captured identities and child effects remain authoritative.
	child, annotations := DomainCoercionInput(child, target)
	source := child.ResultUsage()
	if unknownNull && target.Type.IsDeferredRecord() && source == target {
		// This is NULL transport, not a general RECORD cast/coercion edge.
		return child, nil
	}
	if outerPolicy.stringInput && target.Type.ID() == sqltype.Char && source == target {
		// The literal/input path already applied the CHAR codec. Retain that
		// resolved datum without admitting CHAR to general explicit coercion.
		return child, nil
	}
	pos := raw.Pos
	if start := child.SourcePosition(); start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
		pos = start
	}
	info, err := b.coercionInfo(b.Info(target, pos, child.Properties()), child)
	if err != nil {
		return nil, err
	}
	target = info.Usage
	coercionPlan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
	if err != nil {
		if sqltype.IsFTS(source.Type.BaseType().ID()) || sqltype.IsFTS(target.Type.BaseType().ID()) || source.Type.BaseType().ID() == sqltype.Box || source.Type.BaseType().ID() == sqltype.Point || source.Type.BaseType().ID() == sqltype.Line || source.Type.BaseType().ID() == sqltype.Lseg || target.Type.BaseType().ID() == sqltype.Box || target.Type.BaseType().ID() == sqltype.Point || target.Type.BaseType().ID() == sqltype.Line || target.Type.BaseType().ID() == sqltype.Lseg {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
		}
		if source.Type.BaseType().ID() == sqltype.Tid || target.Type.BaseType().ID() == sqltype.Tid {
			if raw.Syntax == parser.FunctionCast {
				return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
			}
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
		}
		if sqltype.IsRoutineAlias(source.Type.BaseType().ID()) || sqltype.IsRoutineAlias(target.Type.BaseType().ID()) {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
		}
		// A rejected cast from an existing stored scalar to an existing array is
		// a type mismatch, not an unimplemented scalar provider. This only
		// selects the diagnostic below; it adds no coercion capability.
		if sqltype.IsRegObjectAlias(source.Type.BaseType().ID()) || sqltype.IsRegObjectAlias(target.Type.BaseType().ID()) {
			if raw.Syntax == parser.FunctionCast {
				return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
			}
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
		}
		if source.Type.BaseType().ID() == sqltype.Circle || target.Type.BaseType().ID() == sqltype.Circle || source.Type.BaseType().IsCube() || target.Type.BaseType().IsCube() || source.Type.BaseType().IsCitext() || target.Type.BaseType().IsCitext() {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
		}
		if !source.Type.BaseType().IsQueryInt() && !target.Type.BaseType().IsQueryInt() && !sqltype.IsTransactionIdentifier(source.Type.BaseType().ID()) && !sqltype.IsTransactionIdentifier(target.Type.BaseType().ID()) && !b.Consumer.DatetimeZoneOperands && !sqltype.IsNetwork(source.Type.BaseType().ID()) && !sqltype.IsNetwork(target.Type.BaseType().ID()) && source.Type.BaseType().ID() != sqltype.Money && target.Type.BaseType().ID() != sqltype.Money && source.Type.BaseType().ID() != sqltype.Regtype && target.Type.BaseType().ID() != sqltype.Regtype && source.Type.BaseType().ID() != sqltype.Regrole && target.Type.BaseType().ID() != sqltype.Regrole && !source.Type.BaseType().IsEnum() && !target.Type.BaseType().IsEnum() && !source.Type.BaseType().IsComposite() && !target.Type.BaseType().IsComposite() && source.Type.BaseType().ID() != sqltype.JSON && target.Type.BaseType().ID() != sqltype.JSON && source.Type.BaseType().ID() != sqltype.JSONB && target.Type.BaseType().ID() != sqltype.JSONB && source.Type.BaseType().ID() != sqltype.JSONPath && target.Type.BaseType().ID() != sqltype.JSONPath && source.Type.ID() != sqltype.PgNodeTree && target.Type.ID() != sqltype.PgNodeTree && !(target.Type.BaseType().IsArray() && sqltype.StoredScalarCastType(source.Type.BaseType().ID())) && (!sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID())) && !sqltype.OIDCastPair(source.Type.ID(), target.Type.ID()) && !sqltype.TemporalCastPair(source.Type.ID(), target.Type.ID()) {
			if !sqltype.IsOperatorAlias(source.Type.BaseType().ID()) && !sqltype.IsOperatorAlias(target.Type.BaseType().ID()) && !sqltype.IsRegTS(source.Type.BaseType().ID()) && !sqltype.IsRegTS(target.Type.BaseType().ID()) {
				return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
			}
		}
		if raw.Syntax == parser.FunctionCast {
			return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
		}
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), raw.Pos)
	}
	if unsupportedTarget {
		return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	if coercionPlan.RequiresCatalogOutput() {
		b.Prepared.RegclassConcat = true
	}
	if source == target {
		return child, nil
	}
	info.Property = CoercionProperties(child, coercionPlan)
	if folded, ok, err := b.foldRegtypeConstant(child, coercionPlan, info); ok || err != nil {
		return RestoreDomainCastCollations(annotations, folded), err
	}
	return RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}), nil
}

// Strong column/function names survive standard casts; weak literal names do not.
func expressionName(raw parser.Expr, usage sqltype.Usage, bound expr.Expr) (string, bool) {
	for {
		coercion, ok := bound.(plan.CoercionExpr)
		if !ok {
			break
		}
		bound = coercion.Child
	}
	switch r := raw.(type) {
	case parser.RowExpr:
		return "row", false
	case parser.FieldExpr:
		return r.Field.Text, true
	case parser.StarExpr:
		if len(r.Qualifier.Parts) != 0 {
			return r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text, true
		}
		return "?column?", false
	case parser.SubqueryExpr:
		if r.Kind == parser.SubqueryExists {
			return "exists", true
		}
		if r.Kind == parser.SubqueryScalar {
			if child, ok := bound.(plan.SubqueryExpr); ok && len(child.QueryPlan.Root.Schema()) == 1 {
				name := child.QueryPlan.Root.Schema()[0].Name
				return name, name != "?column?" || len(r.Query.Labels) > 0 && r.Query.Labels[0].Text != ""
			}
		}
		return "?column?", false
	case parser.IdentityExpr:
		return r.Name, true
	case parser.CurrentDateTimeExpr:
		return r.Kind.Name(), true
	case parser.GroupingExpr:
		return "grouping", true
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			var otherwise expr.Expr
			if expr, ok := bound.(plan.CaseExpr); ok {
				otherwise = expr.Otherwise
			}
			if name, strong := expressionName(r.Else, usage, otherwise); strong {
				return name, true
			}
		}
		return "case", false
	case parser.ColumnExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.MergeActionExpr:
		return "merge_action", true
	case parser.SQLJSONQueryExpr:
		return r.Kind, true
	case parser.SQLJSONExpr:
		return r.Kind, true
	case parser.JSONPredicateExpr:
		return "?column?", false
	case parser.FunctionExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.CallExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.RegclassExpr:
		if name, strong := expressionName(r.Child, usage, bound); strong {
			return name, true
		}
		return "regclass", false
	case parser.ArrayExpr:
		return "array", false
	case parser.SubscriptExpr:
		return expressionName(r.Base, usage, bound)
	case parser.TimeZoneExpr:
		return "timezone", true
	case parser.CollateExpr:
		if expr, ok := bound.(plan.CollationExpr); ok {
			bound = expr.Child
		}
		return expressionName(r.Child, usage, bound)
	case parser.CastExpr:
		if r.Syntax == parser.FunctionCast {
			return r.Callee, true
		}
		if name, strong := expressionName(r.Child, usage, bound); strong {
			return name, true
		}
		return usage.Type.Name(), false
	case parser.TypedLiteralExpr:
		return usage.Type.Name(), false
	default:
		return "?column?", false
	}
}
func literalInputError(err error, raw parser.LiteralExpr) error {
	return InputErrorAt(err, raw.Value.Pos)
}

// Full analysis invalidation recollects temporal scalar/array captures while the
// candidate still supplies fixed signatures and immutable native logical values.
func (b *Binder) collectTemporalConstants() bool {
	if !b.Expected.Held || b.Expected.RefreshTemporals {
		return true
	}
	owner, generation, ok := b.Host.PlanOwner()
	return ok && b.Expected.PlanOwner == owner && b.Expected.PlanGeneration != generation
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *Binder) CaptureAt(offset int) (TemporalConstant, bool, error) {
	if b.Expected.Held {
		captures := b.Expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.Expected.RefreshTemporals && RefreshableTemporal(captures[i]) {
				return TemporalConstant{}, false, nil
			}
			saved := captures[i]
			for i++; i < len(captures) && captures[i].Offset == offset; i++ {
				if captures[i].TypeID != saved.TypeID || captures[i].Typmod != saved.Typmod ||
					captures[i].Typed != saved.Typed || captures[i].TypedTypmod != saved.TypedTypmod {
					return TemporalConstant{}, false, fmt.Errorf("stuntgres: ambiguous captured temporal metadata")
				}
			}
			return saved, true, nil
		}
	}
	return TemporalConstant{}, false, nil
}

func (b *Binder) Captured(offset int, usage sqltype.Usage) (TemporalConstant, bool, error) {
	if b.Expected.Held {
		captures := b.Expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.Expected.RefreshTemporals && RefreshableTemporal(captures[i]) {
				return TemporalConstant{}, false, nil
			}
			for ; i < len(captures) && captures[i].Offset == offset; i++ {
				capture := captures[i]
				if capture.TypeID == usage.Type.ID() && capture.Typmod == usage.Typmod {
					return capture, true, nil
				}
			}
			return TemporalConstant{}, false, fmt.Errorf("stuntgres: captured temporal usage changed")
		}
	}
	return TemporalConstant{}, false, nil
}
func (b *Binder) InputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if err := b.CaptureTypeDiagnosticInput(u.Type); err != nil {
		return sqltype.Value{}, err
	}
	if u.Type.ContainsRegObject() {
		return b.regObjectConstant(text, u, pos)
	}
	if u.Type.ContainsRegproc() {
		return b.RoutineConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regtype {
		return b.RegtypeConstant(text, pos)
	}
	if u.Type.ContainsRegTS() {
		return b.RegTSConstant(text, u, pos)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) {
		return b.OperatorConstant(text, u.Type, pos)
	}
	if u.Type.ContainsOperatorAlias() {
		return b.operatorCompoundConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text, Pos: pos}})
		if err != nil {
			return sqltype.Value{}, err
		}
		return node.(plan.LiteralExpr).Value, nil
	}
	if u.Type.BaseType().ID() == sqltype.Polygon || u.Type.BaseType().ID() == sqltype.Circle || sqltype.IsFTS(u.Type.BaseType().ID()) || u.Type.BaseType().IsCube() || u.Type.BaseType().IsQueryInt() || u.Type.BaseType().IsCitext() || u.Type.ContainsACLItem() || u.Type.ContainsRegrole() || u.Type.BaseType().IsEnum() || u.Type.BaseType().IsArray() || u.Type.BaseType().IsComposite() || u.Type.BaseType().ID() == sqltype.DateRange || u.Type.BaseType().ID() == sqltype.TimestampRange || u.Type.BaseType().ID() == sqltype.TimestamptzRange {
		if u.Type.BaseType().IsArray() && u.Type.ContainsRegtype() {
			return b.regtypeArrayConstant(text, u, pos)
		}
		c, found, err := b.Captured(pos.Offset, u)
		if err != nil {
			return sqltype.Value{}, err
		}
		if found {
			catalog := b.BindingCatalog()
			if hasEnumInput(u.Type) && (b.Expected.CatalogOwner != catalog.Owner() || b.Expected.CatalogEpoch != catalog.Epoch()) {
				if err := b.Expected.CheckLifetime(catalog.Owner(), catalog.Epoch()); err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.Value{}, ids.ErrCatalogReplaced
			}
			return c.Value.Retag(u.Type)
		}
		if u.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return sqltype.Value{}, err
			}
		}
		value, err := b.decodeLiteralInput(text, u.Type, -1, b.Input.LiteralInput(pos.Character))
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(pos.Character)
				return sqltype.Value{}, &copy
			}
			return sqltype.Value{}, err
		}
		if b.collectTemporalConstants() {
			i := sort.Search(len(b.Prepared.Temporals), func(i int) bool { return b.Prepared.Temporals[i].Offset >= pos.Offset })
			b.Prepared.Temporals = append(b.Prepared.Temporals, TemporalConstant{})
			copy(b.Prepared.Temporals[i+1:], b.Prepared.Temporals[i:])
			b.Prepared.Temporals[i] = TemporalConstant{Offset: pos.Offset, Typmod: u.Typmod, TypeID: u.Type.ID(), Value: value}
		}
		return value, nil
	}
	if u.Type.ID() != sqltype.Interval && u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date && u.Type.ID() != sqltype.Timestamptz && u.Type.ID() != sqltype.Timetz {
		return b.decodeLiteralInput(text, u.Type, -1, b.Input)
	}
	c, found, err := b.Captured(pos.Offset, u)
	if err != nil {
		return sqltype.Value{}, err
	}
	if found {
		if c.TypeID == sqltype.Timetz {
			return sqltype.TimetzValue(u.Type, c.Scalar, c.West)
		}
		if c.TypeID == sqltype.Interval {
			return sqltype.IntervalValue(u.Type, c.Months, c.Days, c.Scalar)
		}
		if c.TypeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.Scalar)
		}
		if c.TypeID == sqltype.Date {
			return sqltype.DateValue(u.Type, c.Scalar)
		}
		return sqltype.TimestampValue(u.Type, c.Scalar)
	}
	inputTypmod := int32(-1)
	if u.Type.ID() == sqltype.Interval {
		inputTypmod = u.Typmod
	}
	v, err := b.decodeLiteralInput(text, u.Type, inputTypmod, b.Input)
	if err == nil && u.Typmod >= 0 {
		var p sqltype.CoercionPlan
		p, err = sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Assignment)
		if err == nil {
			v, err = p.Apply(b.Ctx, v, b.Input, sqltype.OutputEnvironment{Zone: b.Input.Zone, DateStyle: b.Input.DateStyle, IntervalStyle: b.Input.IntervalStyle})
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if b.collectTemporalConstants() {
		var n int64
		var months, days int32
		var err error
		if u.Type.ID() == sqltype.Interval {
			months, days, n, err = v.Interval()
		} else if u.Type.ID() == sqltype.Time || u.Type.ID() == sqltype.Timetz {
			n, err = v.Time()
		} else if u.Type.ID() == sqltype.Date {
			n, err = v.Date()
		} else {
			n, err = v.Timestamp()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		// Symmetric comparison can contextualize an earlier unknown literal
		// after its right operand. Keep captures ordered for rebind lookup.
		i := sort.Search(len(b.Prepared.Temporals), func(i int) bool { return b.Prepared.Temporals[i].Offset >= pos.Offset })
		b.Prepared.Temporals = append(b.Prepared.Temporals, TemporalConstant{})
		copy(b.Prepared.Temporals[i+1:], b.Prepared.Temporals[i:])
		west := int32(0)
		if u.Type.ID() == sqltype.Timetz {
			west, err = v.TimeZoneOffset()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		b.Prepared.Temporals[i] = TemporalConstant{Offset: pos.Offset, Scalar: n, Typmod: u.Typmod, TypeID: u.Type.ID(), West: west, Months: months, Days: days}
	}
	return v, nil
}

func (b *Binder) captureElementInput() error {
	if b.Input.Regclass != nil && b.Input.Regtype != nil && b.Input.Regrole != nil && b.Input.Operator != nil && b.Input.Regproc != nil && b.Input.Regcollation != nil && b.Input.Regnamespace != nil && b.Input.RegTS != nil {
		return nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return err
	}
	return b.Host.CaptureElementInput(auth, &b.Input)
}

// Unknown state exists only during analysis. It never becomes an executable Value.
type ComparisonOperand struct {
	Node expr.Expr
	raw  parser.Expr
}

func (o ComparisonOperand) descriptor() *sqltype.Descriptor {
	if o.Node == nil {
		return nil
	}
	return o.Node.ResultUsage().Type
}

// operandArgument hands an analyzed comparison operand to functions bind
// code; CoerceArgument rebuilds the operand from Expression and Raw.
func operandArgument(o ComparisonOperand) functions.Argument {
	argument := functions.Argument{Expression: o.Node, Raw: o.raw}
	if o.Node != nil {
		argument.Usage = o.Node.ResultUsage()
	}
	return argument
}

func (b *Binder) comparisonOperand(raw parser.Expr) (ComparisonOperand, error) {
	return b.comparisonOperandPolicy(raw, ExpressionPolicy{Context: ProjectionContext})
}

// AnalyzeArgument retains generic binding and occurrence-owned unknown inference.
// The request is closed: children cannot invoke catalog or parameter services.
func (b *Binder) AnalyzeArgument(raw parser.Expr, request functions.AnalysisRequest) (functions.Argument, error) {
	var operand ComparisonOperand
	var err error
	switch request.Intent {
	case functions.ComparisonArgument:
		operand, err = b.comparisonOperand(raw)
	case functions.ThresholdArrayArgument:
		operand, err = b.comparisonOperandPolicy(raw, ExpressionPolicy{Context: ProjectionContext, thresholdArrayInput: true})
	case functions.ZoneArgument:
		// Only explicit casts read the flag; restoring it keeps nested and
		// adjacent calls unaffected.
		previous := b.Consumer.DatetimeZoneOperands
		b.Consumer.DatetimeZoneOperands = true
		operand, err = b.comparisonOperand(raw)
		b.Consumer.DatetimeZoneOperands = previous
	case functions.StringTextArgument, functions.StringOtherArgument:
		operand, err = b.StringOperand(raw, request.Intent == functions.StringTextArgument)
	case functions.RecordBaseArgument:
		previous := b.Clause.JSONBRecordBase
		b.Clause.JSONBRecordBase = true
		operand, err = b.StringOperand(raw, false)
		b.Clause.JSONBRecordBase = previous
	case functions.StringProjectionArgument, functions.ProjectionArgument:
		operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext, stringInput: request.Intent == functions.StringProjectionArgument})
		operand.raw = raw
		if err == nil && unknownNullSyntax(raw) {
			if literal, ok := operand.Node.(plan.LiteralExpr); ok {
				literal.UnknownNull = true
				operand.Node = literal
			}
		}
	case functions.BareProjectionArgument:
		operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
		operand.raw = raw
	case functions.DirectDeferredArgument:
		if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			operand.raw = raw
		} else {
			operand.Node, err = b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
			operand.raw = raw
		}
	default:
		return functions.Argument{}, fmt.Errorf("stuntgres: invalid function argument analysis")
	}
	original, _ := b.OriginalArgument(operand.raw)
	argument := functions.Argument{Expression: operand.Node, Raw: original}
	if operand.Node != nil {
		argument.Usage = operand.Node.ResultUsage()
		if argument.Usage.Type == nil {
			argument.Kind = functions.UnresolvedParameterArgument
		}
	} else if literal, ok := operand.raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.NullValue {
		argument.Kind = functions.DeferredNullArgument
	} else {
		argument.Kind = functions.DeferredStringArgument
	}
	return argument, err
}

func (b *Binder) CoerceArgument(argument functions.Argument, request functions.CoercionRequest) (expr.Expr, error) {
	operand := ComparisonOperand{Node: argument.Expression, raw: argument.Raw}
	switch request.Intent {
	case functions.FinalizeArgument:
		return b.finalizeOperand(operand, request.Target.Type)
	case functions.LikeArgument:
		return b.likeOperand(operand, request.Target.Type)
	case functions.RecordFormalArgument:
		var record *sqltype.Descriptor
		if parameter, ok := argument.Expression.(plan.ParameterExpr); ok {
			record = b.Params.Parameters[parameter.Number].Type
		}
		if record == nil {
			var err error
			record, err = sqltype.RecordShape(nil)
			if err != nil {
				return nil, err
			}
		}
		return b.finalizeOperand(operand, record)
	case functions.SizeFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		if node.ResultUsage() == request.Target {
			return node, nil
		}
		info, err := b.coercionInfo(b.Info(request.Target, node.SourcePosition(), node.Properties()), node)
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		info.Property = CoercionProperties(node, coercionPlan)
		return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
	case functions.ConditionalArgument:
		return b.CoerceConditional(operand, request.Target, request.Label)
	case functions.JSONFormattedArgument, functions.TypeNameCastArgument:
		node := argument.Expression
		var err error
		if request.Intent == functions.TypeNameCastArgument && argument.Usage.Type == nil {
			node, err = b.finalizeOperand(operand, request.Target.Type)
		} else {
			var info expr.Info
			info, err = b.coercionInfo(b.Info(request.Target, node.SourcePosition(), CoercionProperties(node, request.Plan)), node)
			if err == nil {
				node = plan.CoercionExpr{Info: info, Child: node, Plan: request.Plan, Mode: sqltype.Explicit}
			}
		}
		if err != nil {
			return nil, err
		}
		if request.Intent == functions.TypeNameCastArgument && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
			// Resolution selected a cast, not an executable routine. Synchronize
			// the frozen recipe with that selection while retaining the written
			// child; later overloads must not reinterpret the call on replay.
			if b.Stored.Syntax == nil {
				b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
			}
			target := request.Target.Type
			b.Stored.Syntax[StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: request.TypeNameCallPosition}] = parser.CastExpr{
				Child: argument.Raw,
				TypeName: parser.TypeName{
					Name: target.Name(), Schema: &parser.Identifier{Text: target.NamespaceName()}, Pos: request.TypeNameCallPosition,
				},
				Syntax: parser.StandardCast, Pos: request.TypeNameCallPosition,
			}
		}
		return node, nil
	case functions.BareFormalArgument:
		node := argument.Expression
		if node == nil || !request.Planned {
			return nil, fmt.Errorf("stuntgres: invalid bare function argument coercion")
		}
		properties := CoercionProperties(node, request.Plan)
		if request.PreserveProperties {
			properties = node.Properties()
		}
		return plan.CoercionExpr{Info: b.Info(request.Target, argument.Raw.SourcePosition(), properties), Child: node, Plan: request.Plan, Mode: sqltype.Implicit}, nil
	case functions.AssignmentFormalArgument:
		if !b.Clause.SelectedCallArguments || argument.Expression == nil || argument.Usage.Type == nil {
			return b.Bind(argument.Raw, ExpressionPolicy{Context: AssignmentContext, Expected: request.Target, Mode: sqltype.Implicit})
		}
	case functions.DirectFormalArgument:
		if argument.Usage.Type == nil {
			return b.Bind(argument.Raw, ExpressionPolicy{Context: FunctionArgumentContext, Expected: request.Target, Mode: sqltype.Implicit})
		}
	case functions.ExactFormalArgument, functions.TextFormalArgument, functions.CollatedTextFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		argument.Expression, argument.Usage = node, node.ResultUsage()
		if request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text && argument.Usage.Collation.ID != 0 {
			request.Target.Collation = argument.Usage.Collation
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid function argument coercion")
	}
	node := argument.Expression
	if argument.Usage == request.Target {
		return node, nil
	}
	coercionPlan := request.Plan
	var err error
	if !request.Planned {
		coercionPlan, err = sqltype.PlanCoercion(argument.Usage, request.Target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	properties := node.Properties()
	if !request.PreserveProperties {
		properties = CoercionProperties(node, coercionPlan)
	}
	pos := node.SourcePosition()
	if request.AtRaw {
		pos = argument.Raw.SourcePosition()
	}
	info := b.Info(request.Target, pos, properties)
	switch {
	case request.Intent == functions.CollatedTextFormalArgument:
		info, err = b.CollatedInfo(info, node)
	case request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text:
		info, err = b.coercionInfo(info, node)
	}
	if err == nil && info.RoutineReference == nil {
		info, err = b.coercionRoutineInfo(info, node)
	}
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
}

func (b *Binder) comparisonOperandPolicy(raw parser.Expr, policy ExpressionPolicy) (ComparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return ComparisonOperand{raw: raw}, nil
	}
	if annotation, ok := raw.(parser.CastExpr); ok && unknownNullSyntax(raw) {
		var target sqltype.Usage
		var err error
		if policy.thresholdArrayInput {
			target, err = b.Host.ResolveAnnotationTypeInput(annotation.TypeName, true, false, false, false, true)
		} else if policy.stringInput {
			target, err = b.Host.ResolveStringAnnotationType(annotation.TypeName, true)
		} else {
			target, err = b.Host.ResolveAnnotationType(annotation.TypeName, true)
		}
		if err != nil {
			return ComparisonOperand{}, err
		}
		if target.Type.ID() == sqltype.Unknown {
			return ComparisonOperand{raw: annotation.Child}, nil
		}
	}
	switch raw.(type) {
	case parser.InListExpr, parser.ArrayComparisonExpr:
	case parser.RowExpr, parser.FieldExpr, parser.StarExpr, parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.CollateExpr, parser.TimeZoneExpr, parser.OperatorExpr, parser.CallExpr, parser.RegclassExpr, parser.ArrayExpr, parser.SubscriptExpr, parser.FunctionExpr, parser.AggregateExpr, parser.GroupingExpr, parser.CaseExpr, parser.ConditionalExpr, parser.LogicalExpr, parser.PredicateExpr, parser.IndexSubqueryExpr, parser.SubqueryExpr:
	case parser.SQLJSONExpr, parser.SQLJSONQueryExpr, parser.JSONPredicateExpr:
	case parser.MergeActionExpr:
	default:
		return ComparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.Bind(raw, policy)
	return ComparisonOperand{Node: node, raw: raw}, err
}

func (b *Binder) CoerceParameter(p plan.ParameterExpr, u sqltype.Usage) (expr.Expr, error) {
	if existing := b.Params.Parameters[p.Number]; existing.Type != nil && existing.Type != u.Type {
		d := expr.ErrorAt("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.Number), p.Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.Params.Parameters[p.Number] = u.Type.Usage()
	return plan.ParameterExpr{Info: b.Info(u.Type.Usage(), p.Pos, p.Property), Number: p.Number}, nil
}

func (b *Binder) finalizeOperand(o ComparisonOperand, typ *sqltype.Descriptor) (expr.Expr, error) {
	if o.descriptor() != nil {
		return o.Node, nil
	}
	if p, ok := o.Node.(plan.ParameterExpr); ok {
		return b.CoerceParameter(p, typ.Usage())
	}
	if o.raw == nil {
		return nil, fmt.Errorf("stuntgres: missing unknown comparison operand")
	}
	return b.Bind(o.raw, ExpressionPolicy{Context: ProjectionContext, Expected: typ.Usage()})
}

func (b *Binder) BindComparison(raw parser.OperatorExpr) (expr.Expr, error) {
	return b.bindComparisonCandidates(raw, nil)
}

func (b *Binder) bindComparisonCandidates(raw parser.OperatorExpr, candidates *operatorCandidateSet) (expr.Expr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	operator, ok := ComparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	if left, ok := raw.Args[0].(parser.RowExpr); ok {
		if right, ok := raw.Args[1].(parser.RowExpr); ok {
			return b.bindRowComparison(raw, operator, left, right)
		}
	}
	// Copy each occurrence's then-known parameter type before resolving either side.
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	return b.bindComparisonOperandCandidates(raw, operator, left, right, candidates)
}

func (b *Binder) BindComparisonOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right ComparisonOperand) (expr.Expr, error) {
	return b.bindComparisonOperandCandidates(raw, operator, left, right, nil)
}

func (b *Binder) bindComparisonOperandCandidates(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right ComparisonOperand, candidates *operatorCandidateSet) (expr.Expr, error) {
	lt, rt, err := b.resolveComparisonCandidates(raw, left.descriptor(), right.descriptor(), candidates)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	declaration, _, err := b.comparisonRoutineDeclaration(name, lt, rt)
	if err != nil {
		return nil, err
	}
	// VARCHAR has binary-compatible TEXT operator formals, but a contextual
	// literal still owns its original concrete usage for stored CHECK replay.
	// Keep that source node and put the conversion on this occurrence.
	contextualize := func(operand ComparisonOperand, source, target *sqltype.Descriptor) (ComparisonOperand, error) {
		if operand.Node == nil && source != nil && source.BaseType().ID() == sqltype.VarChar && target.ID() == sqltype.Text {
			node, err := b.finalizeOperand(operand, source.BaseType())
			operand.Node = node
			return operand, err
		}
		return operand, nil
	}
	left, err = contextualize(left, right.descriptor(), lt)
	if err != nil {
		return nil, err
	}
	right, err = contextualize(right, left.descriptor(), rt)
	if err != nil {
		return nil, err
	}
	l, err := b.CoerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	r, err := b.CoerceConditional(right, rt.Usage(), name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type, b.Type(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, functions.ComparisonError(b, err, left.descriptor(), right.descriptor(), raw)
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	p.DomainDependent = p.DomainDependent || rp.DomainDependent
	p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	if rp.Volatility > p.Volatility {
		p.Volatility = rp.Volatility
	}
	if predicate.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, p)
	if declaration != nil {
		info, err = b.selectedBuiltinRoutineInfo(info, declaration)
		if err != nil {
			return nil, err
		}
	}
	return plan.ComparisonExpr{Info: info, Left: l, Right: r, Predicate: predicate, Operator: name, Namespace: namespace}, nil
}

func arithmeticName(name string) bool {
	return name == "+" || name == "-" || name == "*" || name == "/" || name == "%" || name == "^"
}
func symbolicArithmeticName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, c := range name {
		if c != '+' && c != '-' && c != '*' && c != '/' && c != '%' && c != '^' {
			return false
		}
	}
	return true
}
func (b *Binder) BindArithmetic(raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid arithmetic expression")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Only syntactic signed constants normalize. Other unary expressions keep
	// their provider tree and runtime overflow/error phase.
	if (name == "+" || name == "-") && len(raw.Args) == 1 && len(raw.Name.Parts) == 1 {
		if literal, ok := raw.Args[0].(parser.LiteralExpr); ok {
			switch literal.Value.Kind {
			case parser.IntegerValue:
				if name == "-" && literal.Value.Integer != -1<<63 {
					literal.Value.Integer = -literal.Value.Integer
				} else if name == "-" {
					break
				}
				literal.Value.Pos = raw.Pos
				return b.Bind(literal, ExpressionPolicy{Context: ProjectionContext})
			case parser.NumericValue:
				if name == "-" {
					if strings.HasPrefix(literal.Value.Text, "-") {
						literal.Value.Text = strings.TrimPrefix(literal.Value.Text, "-")
					} else {
						literal.Value.Text = "-" + literal.Value.Text
					}
				}
				return b.Bind(literal, ExpressionPolicy{Context: ProjectionContext})
			}
		}
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	var right ComparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperand(raw.Args[1])
		if err != nil {
			return nil, err
		}
	}
	return b.finishArithmetic(raw, left, right)
}

func (b *Binder) finishArithmetic(raw parser.OperatorExpr, left, right ComparisonOperand) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operator := arithmeticOperator(name)
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	visible, err := b.builtinVisible(raw.Name, OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if name == "-" && len(raw.Args) == 2 {
		if node, selected, err := functions.SelectJSONBDelete(b, raw, operandArgument(left), operandArgument(right)); selected {
			return node, err
		}
	}
	if symbolicArithmeticName(name) {
		return nil, functions.OperatorError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	signature, err := sqltype.ResolveArithmetic(operator, lt, rt, len(raw.Args) == 1)
	if err != nil {
		return nil, functions.OperatorError(err, lt, rt, raw)
	}
	typ := b.Type(signature.Result)
	leftType := b.Type(signature.Left)
	var rightType *sqltype.Descriptor
	if len(raw.Args) == 2 {
		rightType = b.Type(signature.Right)
	}
	provider, err := sqltype.BindArithmetic(operator, leftType, rightType, typ)
	if err != nil {
		return nil, err
	}
	finish := func(o ComparisonOperand, target *sqltype.Descriptor) (expr.Expr, error) {
		source := o.descriptor()
		if source == nil {
			source = target
		}
		n, err := b.finalizeOperand(o, source)
		if err != nil {
			return nil, err
		}
		if source != target {
			coercionPlan, err := sqltype.PlanCoercion(n.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			info, err := b.coercionRoutineInfo(b.Info(target.Usage(), n.SourcePosition(), CoercionProperties(n, coercionPlan)), n)
			if err != nil {
				return nil, err
			}
			n = plan.CoercionExpr{Info: info, Child: n, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left, leftType)
	if err != nil {
		return nil, err
	}
	var r expr.Expr
	p := l.Properties()
	if len(raw.Args) == 2 {
		r, err = finish(right, rightType)
		if err != nil {
			return nil, err
		}
		rp := r.Properties()
		if rp.Volatility > p.Volatility {
			p.Volatility = rp.Volatility
		}
		p.RowDependent = p.RowDependent || rp.RowDependent
		p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
		p.StageDependent = p.StageDependent || rp.StageDependent
		p.DomainDependent = p.DomainDependent || rp.DomainDependent
		p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	}
	if provider.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	// Contextual consumers use the cached expression start; signature errors
	// above retain the raw operator token.
	pos := raw.Pos
	for _, child := range [...]expr.Expr{l, r} {
		if child != nil {
			start := child.SourcePosition()
			if start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
				pos = start
			}
		}
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	formalLeft, formalRight := signature.Left, signature.Right
	if len(raw.Args) == 1 {
		formalLeft, formalRight = 0, signature.Left
	}
	info, err := b.builtinOperatorRoutineInfo(b.Info(typ.Usage(), pos, p), name, formalLeft, formalRight)
	if err != nil {
		return nil, err
	}
	return plan.ArithmeticExpr{Info: info, Left: l, Right: r, Provider: provider, Operator: operator, Namespace: namespace}, nil
}
