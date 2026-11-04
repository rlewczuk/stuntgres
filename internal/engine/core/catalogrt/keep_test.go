package catalogrt

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCatalogSourceRejectsInputProof(t *testing.T) {
	var boolean *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Bool {
			boolean = typ
		}
	}
	if boolean == nil {
		t.Fatal("missing builtin Boolean")
	}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12248})
	leaves := []exec.Leaf{{Ref: ref, Table: &parser.TableReference{}}}
	source := &plan.SourceNode{Ref: ref}
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: value}
	}
	root := func(predicate expr.Expr) *plan.ProjectNode {
		return &plan.ProjectNode{Frame: 1, Child: &plan.FilterNode{Child: source, Predicate: predicate}}
	}
	falseExpr := literal(sqltype.Boolean(boolean, false))
	for _, tc := range []struct {
		name      string
		predicate expr.Expr
		rejects   bool
	}{
		{"false", falseExpr, true},
		{"null", literal(sqltype.Null(boolean)), true},
		{"true", literal(sqltype.Boolean(boolean, true)), false},
		{"row", plan.ColumnExpr{}, false},
		{"stage", &plan.WindowExpr{}, false},
		{"subquery", plan.SubqueryExpr{}, false},
		{"arbitrary", plan.LogicalExpr{Op: parser.LogicalAnd, Left: falseExpr, Right: falseExpr}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := KeepQuery{Root: root(tc.predicate), Leaves: leaves}
			got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil)
			if err != nil || got != tc.rejects {
				t.Fatal("safe physical proof only", got, err, tc.rejects)
			}
		})
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 1}
	q := KeepQuery{Root: root(parameter), Leaves: leaves}
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.Boolean(boolean, true), sqltype.Null(boolean)} {
		q.Parameters = []sqltype.Value{value}
		want := value.IsNull()
		if !want {
			truth, err := value.Boolean()
			if err != nil {
				t.Fatal(err)
			}
			want = !truth
		}
		if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); err != nil || got != want {
			t.Fatal("current imported parameter, not cached rejection", got, err, want)
		}
	}
	q.Parameters = nil
	if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); got || err == nil {
		t.Fatal("missing imported parameter error suppressed", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := q.RejectsInput(ctx, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); got || !errors.Is(err, context.Canceled) {
		t.Fatal("original context error suppressed", got, err)
	}

	for _, tc := range []struct {
		name   string
		change func(*KeepQuery, *plan.ProjectNode, *[]exec.Leaf, *plan.RowMark, **exec.LockContext)
	}{
		{"other occurrence", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			(*in)[0].Table = &parser.TableReference{}
		}},
		{"other ref", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			(*in)[0].Ref.Epoch++
		}},
		{"multiple sources", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			*in = append(*in, (*in)[0])
		}},
		{"row mark", func(_ *KeepQuery, _ *plan.ProjectNode, _ *[]exec.Leaf, mark *plan.RowMark, _ **exec.LockContext) {
			mark.Mode = 1
		}},
		{"lock context", func(_ *KeepQuery, _ *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, locks **exec.LockContext) {
			*locks = &exec.LockContext{}
		}},
		{"different frame", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			p.Frame++
		}},
		{"cte boundary", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			p.CTEOwner = true
		}},
		{"rule boundary", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			p.RuleInvocation = true
		}},
		{"nested input", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			p.Child.(*plan.FilterNode).Child = &plan.ProjectNode{Child: source}
		}},
		{"sample", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			sampled := *source
			sampled.Sample = &plan.TableSample{}
			p.Child.(*plan.FilterNode).Child = &sampled
		}},
		{"source occurrence", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
			copied := *source
			copied.Occurrence++
			p.Child.(*plan.FilterNode).Child = &copied
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := root(falseExpr)
			q := KeepQuery{Root: p, Leaves: leaves}
			in := append([]exec.Leaf(nil), leaves...)
			var mark plan.RowMark
			var locks *exec.LockContext
			tc.change(&q, p, &in, &mark, &locks)
			if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, in, 0, mark, locks); got || err != nil {
				t.Fatal("unsupported topology must demand ordinary capture", got, err)
			}
		})
	}

	t.Run("subscription stats conjunction", func(t *testing.T) {
		var relation *catalog.Relation
		for i := range catalogSourceTemplates() {
			template := &catalogSourceTemplates()[i]
			if template.Address().ObjectID == pgcatalog.StatSubscriptionStatsOID {
				relation = template
			}
		}
		if relation == nil {
			t.Fatal("missing registered subscription stats template")
		}
		ref := domain.Ref(1, relation.Address())
		leaves := []exec.Leaf{{Ref: ref, Relation: relation, Table: &parser.TableReference{}}}
		origin := ref
		origin.Address.SubID = relation.Attributes[pgcatalog.StatSubscriptionStatsNameColumn]
		nameUsage := relation.Columns[pgcatalog.StatSubscriptionStatsNameColumn].Usage
		column := plan.ColumnExpr{Info: expr.Info{Usage: nameUsage}, Origin: origin, Scope: 1, Frame: 1, Slot: pgcatalog.StatSubscriptionStatsNameColumn}
		name := plan.ParameterExpr{Info: expr.Info{Usage: nameUsage}, Number: 1}
		predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, nameUsage.Type, nameUsage.Type, boolean, nameUsage.Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		equality := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: column, Right: name, Operator: "=", Predicate: predicate}
		declaration := routine.OperatorRoutine("=", ids.OID(sqltype.Name), ids.OID(sqltype.Name))
		for _, procedure := range routine.BuiltinProcedures(relation.Owner) {
			if procedure.Builtin == declaration {
				captured := procedure
				equality.RoutineReference = &captured
				break
			}
		}
		if declaration == nil || equality.RoutineReference == nil {
			t.Fatal("missing actual builtin NAME equality association")
		}
		conjunction := func(left, right expr.Expr) plan.LogicalExpr {
			return plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalAnd, Left: left, Right: right}
		}
		root := func(predicate expr.Expr) *plan.ProjectNode {
			return &plan.ProjectNode{Frame: 1, Child: &plan.FilterNode{Child: &plan.SourceNode{Ref: ref}, Predicate: predicate}}
		}
		parameters := []sqltype.Value{sqltype.String(nameUsage.Type, "actual_name"), sqltype.Boolean(boolean, false)}
		booleanParameter := plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 2}
		proof := func(q *KeepQuery) (bool, error) {
			return q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil)
		}
		for _, rejection := range []expr.Expr{falseExpr, literal(sqltype.Null(boolean)), booleanParameter} {
			for _, reversed := range []bool{false, true} {
				left, right := expr.Expr(equality), rejection
				if reversed {
					left, right = right, left
				}
				q := KeepQuery{Root: root(conjunction(left, right)), Leaves: leaves, Parameters: parameters}
				if got, err := proof(&q); !got || err != nil {
					t.Fatal("shallow safe NAME equality/rejector", got, err, reversed)
				}
			}
		}
		literalEquality := equality
		literalEquality.Right = plan.LiteralExpr{Info: name.Info, Value: parameters[0]}
		literalQuery := KeepQuery{Root: root(conjunction(literalEquality, falseExpr)), Leaves: leaves}
		if got, err := proof(&literalQuery); !got || err != nil {
			t.Fatal("physical NAME literal needs no imported frame", got, err)
		}
		// Both the equality operands and the Boolean conjunct can be reversed;
		// the column comparison itself is never evaluated by the proof.
		reversed := equality
		reversed.Left, reversed.Right = reversed.Right, reversed.Left
		q := KeepQuery{Root: root(conjunction(reversed, booleanParameter)), Leaves: leaves, Parameters: parameters}
		for _, value := range []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.Null(boolean), sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false)} {
			q.Parameters = []sqltype.Value{parameters[0], value}
			want := value.IsNull()
			if !want {
				truth, _ := value.Boolean()
				want = !truth
			}
			if got, err := proof(&q); got != want || err != nil {
				t.Fatal("current imported Boolean, no retained decision", got, err, want)
			}
		}
		for _, malformed := range [][]sqltype.Value{
			{parameters[0], parameters[0]},
			{sqltype.Boolean(boolean, false), parameters[1]},
		} {
			q.Parameters = malformed
			if got, err := proof(&q); got || err == nil {
				t.Fatal("foreign imported parameter type must remain an error", got, err)
			}
		}
		q.Parameters = parameters[:1]
		if got, err := proof(&q); got || err == nil {
			t.Fatal("missing Boolean parameter must remain an error", got, err)
		}
		q.Parameters = nil
		if got, err := proof(&q); got || err == nil {
			t.Fatal("missing NAME parameter must remain an error", got, err)
		}
		q.Parameters = []sqltype.Value{sqltype.Null(nameUsage.Type), sqltype.Boolean(boolean, false)}
		if got, err := proof(&q); !got || err != nil {
			t.Fatal("typed NAME NULL equality is safe", got, err)
		}
		if got, err := q.RejectsInput(ctx, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); got || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled conjunction cannot publish emptiness", got, err)
		}
		domainName, err := sqltype.Domain(90001, "name_domain", "name_domain", nameUsage, true)
		if err != nil {
			t.Fatal(err)
		}
		domainBoolean, err := sqltype.Domain(90002, "bool_domain", "bool_domain", boolean.Usage(), true)
		if err != nil {
			t.Fatal(err)
		}
		domainColumn := column
		domainColumn.Usage.Type = domainName
		otherGeneration := column
		for _, typ := range sqltype.Builtins() {
			if typ.ID() == sqltype.Name {
				otherGeneration.Usage.Type = typ
			}
		}
		for _, tc := range []struct {
			name   string
			change func(*plan.LogicalExpr, *plan.ComparisonExpr)
		}{
			{"TRUE literal", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) { l.Right = literal(sqltype.Boolean(boolean, true)) }},
			{"domain Boolean", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = plan.LiteralExpr{Info: expr.Info{Usage: domainBoolean.Usage()}, Value: sqltype.Null(domainBoolean)}
			}},
			{"malformed Boolean literal", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: parameters[0]}
			}},
			{"nested AND", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) { l.Right = conjunction(falseExpr, falseExpr) }},
			{"OR", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) { l.Op = parser.LogicalOr }},
			{"NOT", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalNot, Left: falseExpr}
			}},
			{"query right", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) { l.QueryRight = true }},
			{"subquery", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = plan.SubqueryExpr{Info: expr.Info{Usage: boolean.Usage()}}
			}},
			{"arbitrary rejector", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = unevaluatedKeepProof{Info: expr.Info{Usage: boolean.Usage()}}
			}},
			{"effectful counterpart", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Left = unevaluatedKeepProof{Info: expr.Info{Usage: boolean.Usage()}}
			}},
			{"two rejecting constants", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) { l.Left = falseExpr }},
			{"coerced Boolean", func(l *plan.LogicalExpr, _ *plan.ComparisonExpr) {
				l.Right = plan.CoercionExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: falseExpr}
			}},
			{"coerced name", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				e.Left = plan.CoercionExpr{Info: column.Info, Child: column}
			}},
			{"coerced probe", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				e.Right = plan.CoercionExpr{Info: name.Info, Child: name}
			}},
			{"NAME function call", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				e.Right = functions.Identity{Info: name.Info, Kind: functions.IdentityCurrentSchema, OrdinaryCall: true}
			}},
			{"domain column", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Left = domainColumn }},
			{"foreign generation", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Left = otherGeneration }},
			{"arbitrary probe", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Right = unevaluatedKeepProof{Info: name.Info} }},
			{"non equality", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Operator = "<>" }},
			{"unbound predicate", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Predicate = sqltype.ComparisonPredicate{} }},
			{"named comparison", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Named = true }},
			{"routine authority", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.RoutineRef = ref }},
			{"routine callback", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.RoutineReference = &catalog.Procedure{} }},
			{"forged builtin association", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				copied := *e.RoutineReference.(*catalog.Procedure)
				forged := *copied.Builtin
				copied.Builtin = &forged
				e.RoutineReference = &copied
			}},
			{"changed builtin identity", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				copied := *e.RoutineReference.(*catalog.Procedure)
				copied.ObjectAddress.ObjectID++
				e.RoutineReference = &copied
			}},
			{"changed builtin volatility", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				copied := *e.RoutineReference.(*catalog.Procedure)
				copied.Volatility = expr.Volatile
				e.RoutineReference = &copied
			}},
			{"changed builtin strictness", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				copied := *e.RoutineReference.(*catalog.Procedure)
				copied.Strict = false
				e.RoutineReference = &copied
			}},
			{"other namespace", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Namespace = "public" }},
			{"volatile", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Property.Volatility = expr.Volatile }},
			{"domain dependency", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Property.DomainDependent = true }},
			{"runtime domain", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Property.RuntimeDomainConversion = true }},
			{"stage dependency", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { e.Property.StageDependent = true }},
			{"virtual column", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				c := column
				c.Virtual = unevaluatedKeepProof{Info: c.Info}
				e.Left = c
			}},
			{"wrong column", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.Slot = 0; e.Left = c }},
			{"wrong attribute", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.Origin.Address.SubID++; e.Left = c }},
			{"wrong epoch", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.Origin.Epoch++; e.Left = c }},
			{"wrong owner", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) {
				c := column
				c.Origin = (&ids.Domain{}).Ref(ref.Epoch, c.Origin.Address)
				e.Left = c
			}},
			{"wrong frame", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.Frame++; e.Left = c }},
			{"wrong occurrence", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.QueryOccurrence = 2; e.Left = c }},
			{"wrong scope", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.Scope++; e.Left = c }},
			{"merged column", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.MergeSource = true; e.Left = c }},
			{"row column", func(_ *plan.LogicalExpr, e *plan.ComparisonExpr) { c := column; c.RowWidth = 1; e.Left = c }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				e := equality
				logical := conjunction(e, falseExpr)
				tc.change(&logical, &e)
				if _, ok := logical.Left.(plan.ComparisonExpr); ok {
					logical.Left = e
				}
				q := KeepQuery{Root: root(logical), Leaves: leaves, Parameters: parameters}
				if got, err := proof(&q); got || err != nil {
					t.Fatal("unsupported expression must retain ordinary demand without evaluation", got, err)
				}
			})
		}
		for _, tc := range []struct {
			name   string
			change func(*plan.ProjectNode, *[]exec.Leaf, *plan.RowMark, **exec.LockContext)
		}{
			{"other table", func(_ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				(*in)[0].Table = &parser.TableReference{}
			}},
			{"other ref", func(_ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				(*in)[0].Ref.Epoch++
			}},
			{"other descriptor", func(_ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				copied := *relation
				(*in)[0].Relation = &copied
			}},
			{"multiple sources", func(_ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				*in = append(*in, (*in)[0])
			}},
			{"row mark", func(_ *plan.ProjectNode, _ *[]exec.Leaf, mark *plan.RowMark, _ **exec.LockContext) { mark.Mode = 1 }},
			{"locks", func(_ *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, locks **exec.LockContext) {
				*locks = &exec.LockContext{}
			}},
			{"frame", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.Frame++ }},
			{"CTE", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.CTEOwner = true }},
			{"rule", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				p.RuleInvocation = true
			}},
			{"sort boundary", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				p.Child = &plan.SortNode{Child: p.Child}
			}},
			{"sample", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				p.Child.(*plan.FilterNode).Child.(*plan.SourceNode).Sample = &plan.TableSample{}
			}},
			{"source occurrence", func(p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) {
				p.Child.(*plan.FilterNode).Child.(*plan.SourceNode).Occurrence++
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p := root(conjunction(equality, falseExpr))
				q := KeepQuery{Root: p, Leaves: leaves, Parameters: parameters}
				in := append([]exec.Leaf(nil), leaves...)
				var mark plan.RowMark
				var locks *exec.LockContext
				tc.change(p, &in, &mark, &locks)
				if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, in, 0, mark, locks); got || err != nil {
					t.Fatal("source-owned conjunction must keep topology guards", got, err)
				}
			})
		}
	})
}

type unevaluatedKeepProof struct{ expr.Info }

func (unevaluatedKeepProof) Eval(expr.Context) (sqltype.Value, error) {
	panic("unsupported catalog proof operand evaluated")
}
