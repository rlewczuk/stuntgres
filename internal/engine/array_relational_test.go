package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayRelationalUnitCall(t *testing.T, name, input string, n int64) (functions.ArrayRandom, *EvalContext) {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{arrayInspectUnitArray(input)}
	if name == "array_sample" {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}})
	}
	raw := arrayInspectUnitRaw(name, args...)
	raw.Pos = parser.Position{Offset: 7, Character: 8}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	session.arrayRandom.SeedWords(1, 2)
	c := &EvalContext{Context: context.Background(), statementScope: &StatementScope{op: &Operation{session: session}}}
	return node.(functions.ArrayRandom), c
}
func arrayRelationalUnitText(t *testing.T, v sqltype.Value) string {
	t.Helper()
	text, err := v.Type().Output(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestArrayRelationalRandomSlices(t *testing.T) {
	for _, name := range []string{"array_shuffle", "array_sample"} {
		for _, n := range []int64{1, 2, 3} {
			call, c := arrayRelationalUnitCall(t, name, "[-2:0][5:6]={{1,NULL},{2,20},{3,30}}", n)
			input, _ := call.Args[0].Eval(c)
			for range 32 {
				got, err := call.Eval(c)
				if err != nil {
					t.Fatal(err)
				}
				count, lower := int32(n), int32(1)
				if name == "array_shuffle" {
					count, lower = 3, -2
				}
				if got.ArrayRank() != 2 || got.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: lower, Length: count}) || got.ArrayDimension(1) != (sqltype.ArrayDimension{Lower: 5, Length: 2}) {
					t.Fatal("lost shape")
				}
				selected := map[int64]bool{}
				for i := range int(count) {
					id, err := got.ArrayElement(i * 2).Integer()
					if err != nil || id < 1 || id > 3 || selected[id] {
						t.Fatal("repeated/noninput first-axis occurrence", id, err)
					}
					selected[id] = true
					if !sqltype.SameDatum(got.ArrayElement(i*2+1), input.ArrayElement(int(id-1)*2+1)) {
						t.Fatal("broken first-axis slice")
					}
				}
				after, _ := call.Args[0].Eval(c)
				if after != input || arrayRelationalUnitText(t, after) != "[-2:0][5:6]={{1,NULL},{2,20},{3,30}}" {
					t.Fatal("mutated source")
				}
			}
		}
	}
	// Elements deliberately have no SQL equality operator. Random shape operations
	// retain values directly rather than requesting equality or text conversion.
	call, c := arrayRelationalUnitCall(t, "array_sample", "{1}", 1)
	view := New().CatalogRead()
	json, _ := view.LookupType(sqltype.JSON)
	array, _ := view.LookupType(json.AssociatedArray())
	value, err := array.Input(c.Context, `{"{}",NULL}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	call.Info.Usage = array.Usage()
	call.Args[0] = literalExpression{Info: expr.Info{Usage: array.Usage()}, value: value}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	if got, err := call.Eval(c); err != nil || got.ArrayLen() != 1 {
		t.Fatal(got, err)
	}
}

func TestArrayRelationalSampleCounts(t *testing.T) {
	for _, n := range []int64{-2147483648, -1, 0, 1, 2, 3, 2147483647} {
		call, c := arrayRelationalUnitCall(t, "array_sample", "{{1,2,3},{4,5,6}}", n)
		got, err := call.Eval(c)
		if n < 0 || n > 2 {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22023" || d.Message != "sample size must be between 0 and 2" || d.Position != "" {
				t.Fatal("count diagnostic", err)
			}
		} else {
			if err != nil || got.ArrayLen() != int(n)*3 {
				t.Fatal(got, err)
			}
			if n == 0 && got.ArrayRank() != 0 {
				t.Fatal("zero sample is not canonical empty")
			}
		}
	}
	call, c := arrayRelationalUnitCall(t, "array_sample", "{1}", -1)
	failure := errors.New("count evaluation")
	var order []int
	call.Args[0] = queryProbe(call.Args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
		order = append(order, 0)
		return sqltype.Null(call.Usage.Type), nil
	})
	call.Args[1] = queryProbe(call.Args[1].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { order = append(order, 1); return sqltype.Value{}, failure })
	if _, err := call.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("strictness skipped ordinary argument evaluation", order, err)
	}
	owner, err := c.FunctionArrayRandomOwner()
	if err != nil {
		t.Fatal(err)
	}
	if owner == expr.RandomOwner(&c.statementScope.op.session.random) {
		t.Fatal("seedable stream reused")
	}
}

func arrayRelationalUnitUnnest(t *testing.T, input string) unnestExpression {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	node, err := b.bindUnnest(arrayInspectUnitRaw("unnest", arrayInspectUnitArray(input)))
	if err != nil {
		t.Fatal(err)
	}
	return node.(unnestExpression)
}
func TestArrayRelationalUnnestCursor(t *testing.T) {
	c := &EvalContext{Context: context.Background()}
	e := arrayRelationalUnitUnnest(t, "[-2:-1][5:6]={{1,NULL},{3,4}}")
	var evaluations int
	input, _ := e.args[0].Eval(c)
	e.args[0] = queryProbe(e.args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { evaluations++; return input, nil })
	first, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if first.array != input || second.array != input {
		t.Fatal("copied input payload")
	}
	for i, want := range []any{int64(1), nil, int64(3), int64(4)} {
		got, present, err := first.next(c.Context, e.Usage.Type)
		if err != nil || !present || got.Type() != e.Usage.Type {
			t.Fatal(i, got, present, err)
		}
		if want == nil {
			if !got.IsNull() {
				t.Fatal("lost present NULL")
			}
		} else {
			n, err := got.Integer()
			if err != nil || n != want {
				t.Fatal(n, err)
			}
		}
	}
	if got, present, err := first.next(c.Context, e.Usage.Type); err != nil || present || !got.IsNull() {
		t.Fatal(got, present, err)
	}
	if got, present, err := second.next(c.Context, e.Usage.Type); err != nil || !present || got.IsNull() {
		t.Fatal("shared cursor position", got, present, err)
	}
	if evaluations != 2 {
		t.Fatal("arguments not once per invocation", evaluations)
	}
	for _, input := range []string{"{}", "{1}"} {
		e := arrayRelationalUnitUnnest(t, input)
		if input == "{1}" {
			e.args[0] = literalExpression{Info: expr.Info{Usage: e.args[0].ResultUsage()}, value: sqltype.Null(e.args[0].ResultUsage().Type)}
		}
		cursor, err := openFunctionCall(c, e)
		if err != nil {
			t.Fatal(err)
		}
		if v, present, err := cursor.next(c.Context, e.Usage.Type); err != nil || present || !v.IsNull() || v.Type() != e.Usage.Type {
			t.Fatal(v, present, err)
		}
		cursor.close()
	}
}

func TestArrayRelationalCompositeNull(t *testing.T) {
	view := New().CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	text, _ := view.LookupType(sqltype.Text)
	pair, err := sqltype.Composite(50000, "pair", "pair", 50001, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "label", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(50002, "pair_domain", "pair_domain", pair.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range []*sqltype.Descriptor{pair, domain} {
		array, err := sqltype.Array(50003, "_pairs", element)
		if err != nil {
			t.Fatal(err)
		}
		value, err := array.Input(context.Background(), `{"(1,a)",NULL,"(3,)"}`, -1)
		if err != nil {
			t.Fatal(err)
		}
		e := unnestExpression{Info: expr.Info{Usage: element.Usage(), Property: expr.Properties{StageDependent: true}}, args: []expr.Expr{literalExpression{Info: expr.Info{Usage: array.Usage()}, value: value}}}
		node := &functionFromNode{querySchema: querySchema{{Name: "id", Usage: integer.Usage()}, {Name: "label", Usage: text.Usage()}}, calls: []expr.Expr{e}}
		if err := validateFunctionFromLayout(node); err != nil {
			t.Fatal(err)
		}
		invocation, err := openFunctionInvocation(&EvalContext{Context: context.Background()}, node)
		if err != nil {
			t.Fatal(err)
		}
		row := make([]sqltype.Value, 2)
		for i := range 3 {
			present, err := invocation.nextFrom(context.Background(), node.calls, row)
			if err != nil || !present {
				t.Fatal(i, present, err)
			}
			if row[0].Type() != integer || row[1].Type() != text {
				t.Fatal("logical element lost physical member layout")
			}
			if i == 1 && (!row[0].IsNull() || !row[1].IsNull()) {
				t.Fatal("NULL element not present NULL-member row")
			}
		}
		if present, err := invocation.nextFrom(context.Background(), node.calls, row); err != nil || present || !row[0].IsNull() || !row[1].IsNull() {
			t.Fatal("exhaustion", present, err)
		}
		invocation.close()
	}
}

func TestArrayRelationalZeroWidthComposite(t *testing.T) {
	ctx := context.Background()
	view := New().CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	ordinal, _ := view.LookupType(sqltype.Int8)
	empty, err := sqltype.Composite(50000, "empty", "empty", 50001, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(50002, "empty_domain", "empty_domain", empty.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range []*sqltype.Descriptor{empty, domain} {
		array, err := sqltype.Array(50003, "_empty", element)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []string{"{(),NULL}", "{}"} {
			value, err := array.Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
			call := unnestExpression{Info: expr.Info{Usage: element.Usage(), Property: expr.Properties{StageDependent: true}}, args: []expr.Expr{literalExpression{Info: expr.Info{Usage: array.Usage()}, value: value}}}
			node := &functionFromNode{calls: []expr.Expr{call}}
			if err := validateFunctionFromLayout(node); err != nil {
				t.Fatal("valid zero-column layout", err)
			}
			invocation, err := openFunctionInvocation(&EvalContext{Context: ctx}, node)
			if err != nil {
				t.Fatal(err)
			}
			for i := range value.ArrayLen() + 1 {
				present, err := invocation.nextFrom(ctx, node.calls, nil)
				if err != nil || present != (i < value.ArrayLen()) {
					t.Fatal("zero-field row presence", input, i, present, err)
				}
			}
			invocation.close()
			node.ordinality = true
			node.querySchema = querySchema{{Name: "n", Usage: ordinal.Usage()}}
			if err := validateFunctionFromLayout(node); err != nil {
				t.Fatal("valid zero-field ordinality", err)
			}
			node.querySchema[0].Usage = integer.Usage()
			if err := validateFunctionFromLayout(node); err == nil {
				t.Fatal("accepted non-INT8 ordinality")
			}
			node.ordinality = false
			if err := validateFunctionFromLayout(node); err == nil {
				t.Fatal("accepted unowned schema column")
			}
			node.calls = []expr.Expr{arrayRelationalUnitUnnest(t, "{1}")}
			node.querySchema = nil
			if err := validateFunctionFromLayout(node); err == nil {
				t.Fatal("accepted missing scalar column")
			}
		}
	}
}

func TestArrayRelationalTypedDefinitionClasses(t *testing.T) {
	view := New().CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	named, err := sqltype.Composite(50000, "empty", "empty", 50001, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(50002, "empty_domain", "empty_domain", named.Usage())
	if err != nil {
		t.Fatal(err)
	}
	record, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Identical zero-field physical spans must not erase named-versus-OUT
	// result identity. Domains use the physical base's classification.
	for _, tc := range []struct {
		typ     *sqltype.Descriptor
		message string
	}{
		{integer, `a column definition list is only allowed for functions returning "record"`},
		{named, "a column definition list is redundant for a function returning a named composite type"},
		{domain, "a column definition list is redundant for a function returning a named composite type"},
		{record, "a column definition list is redundant for a function with OUT parameters"},
	} {
		call := literalExpression{Info: expr.Info{Usage: tc.typ.Usage()}, value: sqltype.Null(tc.typ)}
		if got := functionColumnDefinitionMessage(call); got != tc.message {
			t.Fatalf("type %d: got %q, want %q", tc.typ.ID(), got, tc.message)
		}
	}
}

func TestArrayRelationalUsage(t *testing.T) {
	ctx := context.Background()
	engine := New()
	collation := collationUnitDefinition(engine, 71000, "ar_usage_coll")
	stage := engine.BeginCatalogStage(engine.CatalogRead())
	if err := stage.Create(collation); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, engine, stage)
	ref, err := sqltype.RegisteredCollation(uint32(collation.OID), collation.Semantics)
	if err != nil {
		t.Fatal(err)
	}
	view := engine.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	varchar, _ := view.LookupType(sqltype.VarChar)
	intArray, _ := view.LookupType(integer.AssociatedArray())
	varcharArray, _ := view.LookupType(varchar.AssociatedArray())
	domain := func(id sqltype.ID, name string, base sqltype.Usage) *sqltype.Descriptor {
		d, err := sqltype.Domain(id, name, name, base)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	array := func(id sqltype.ID, name string, element *sqltype.Descriptor) *sqltype.Descriptor {
		d, err := sqltype.Array(id, name, element)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	intDomain := domain(50000, "ar_usage_int", integer.Usage())
	intDomainArray := array(50001, "_ar_usage_int", intDomain)
	outerDomain := domain(50002, "ar_usage_array", intArray.Usage())
	wordBase := varchar.Usage()
	wordBase.Typmod = 7 // VARCHAR(3)'s physical typmod.
	wordDomain := domain(50003, "ar_usage_word", wordBase)
	wordArray := array(50004, "_ar_usage_word", wordDomain)
	sizedArray := varcharArray.Usage()
	sizedArray.Typmod = 7
	for _, tc := range []struct {
		name     string
		input    sqltype.Usage
		random   *sqltype.Descriptor
		element  *sqltype.Descriptor
		collated bool
	}{
		{"outer-domain", outerDomain.Usage(), intArray, integer, false},
		{"element-domain", intDomainArray.Usage(), intDomainArray, intDomain, false},
		{"domain-varchar", wordArray.Usage(), wordArray, wordDomain, true},
		{"sized-varchar", sizedArray, varcharArray, varchar, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"array_sample", "array_shuffle", "unnest"} {
				b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
				b.aggregateClause = "SELECT"
				b.relation = &catalog.Relation{Columns: []ColumnDescriptor{{Name: "a", Usage: tc.input}}, Attributes: []int32{1}, Index: map[string]int{"a": 0}}
				var input parser.Expr = parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}}
				position := parser.Position{Character: 20}
				if tc.collated {
					input = parser.CollateExpr{Child: input, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ar_usage_coll"}}}, Pos: position}
				}
				raw := arrayInspectUnitRaw(name, input)
				if name == "array_sample" {
					raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
				}
				var node expr.Expr
				var err error
				if name == "unnest" {
					node, err = b.bindUnnest(raw)
				} else {
					node, err = b.bindFunctionProvider(raw)
				}
				if err != nil {
					t.Fatal(name, err)
				}
				want := tc.random.Usage()
				if name == "unnest" {
					want = tc.element.Usage()
				}
				if tc.collated {
					want.Collation = ref
					if node.ExplicitCollation() != position || node.CollationIndeterminate() {
						t.Fatal(name, "lost explicit collation provenance")
					}
				}
				if node.ResultUsage() != want {
					t.Fatal(name, "logical type/typmod/collation", node.ResultUsage(), "want", want)
				}
			}
		})
	}
}

func TestArrayRelationalExpressionOwners(t *testing.T) {
	call, c := arrayRelationalUnitCall(t, "array_sample", "{1,2}", 1)
	visits := 0
	boundExpressionChildren(call, func(expr.Expr) { visits++ })
	if visits != 2 || plannedPhysicalVolatility(call, false) != expr.Volatile || generationImmutable(call) || !closedTransactionTimeDefault(call) || !closedUUIDDefault(call) {
		t.Fatal("missing closed owner")
	}
	qualified := call
	qualified.Namespace = "pg_catalog"
	if !sameGroupExpression(call, qualified) {
		t.Fatal("namespace spelling changed resolved identity")
	}
	next, changed, err := functions.MapArrayRandomChildren(call, func(child expr.Expr) (expr.Expr, bool, error) { return child, false, nil })
	if err != nil || changed || &next.Args[0] != &call.Args[0] {
		t.Fatal("unchanged child mapping copied")
	}
	for _, f := range []func(*EvalContext, expr.Expr, bool) (expr.Expr, bool, error){simplifyConditional, preflightCoercionsWithPolicy} {
		mapped, _, err := f(c, call, false)
		if err != nil || plannedPhysicalVolatility(mapped, false) != expr.Volatile {
			t.Fatal("lost volatile provider", err)
		}
	}
	spec, err := call.Render()
	if err != nil || spec.Form != expr.FormCall || spec.Name != "array_sample" || spec.Replay.Name != "" || len(spec.Args) != 2 || spec.Replay.Validate() != nil {
		t.Fatal("stored replay", spec, err)
	}
	bad := call
	bad.Args = bad.Args[:1]
	if bad.Validate() == nil {
		t.Fatal("invalid stored arity accepted")
	}
}

func TestArrayRelationalReownership(t *testing.T) {
	source, target := New(), New()
	tb := newTestExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, nil)
	b := newTestExpressionBinder(t, context.Background(), source, sqltype.InputEnvironment{}, nil)
	raw := arrayInspectUnitRaw("array_sample", arrayInspectUnitArray("[-2:0][5:6]={{1,NULL},{2,20},{3,30}}"), parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}})
	raw.Pos = parser.Position{Offset: 7, Character: 8}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	remap := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		return tb.typ(d.ID())
	}
	mapped, changed, err := retagBoundTypes(target.CatalogRead(), node, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	old, next := node.(functions.ArrayRandom), mapped.(functions.ArrayRandom)
	if next.Validate() != nil || old.Usage.Type == next.Usage.Type || old.Args[0].ResultUsage().Type.Element() == next.Args[0].ResultUsage().Type.Element() || old.Args[1].ResultUsage().Type == next.Args[1].ResultUsage().Type || old.SourcePosition() != next.SourcePosition() || old.Properties() != next.Properties() {
		t.Fatal("random generation remap")
	}
	e := arrayRelationalUnitUnnest(t, "{1,NULL}")
	rebound, did, err := retagBoundTypes(target.CatalogRead(), e, remap)
	if err != nil || !did {
		t.Fatal(did, err)
	}
	u := rebound.(unnestExpression)
	if u.Usage.Type != u.args[0].ResultUsage().Type.Element() || u.Usage.Type == e.Usage.Type || u.Properties() != e.Properties() {
		t.Fatal("unnest generation remap")
	}
	if sameQuerySetOccurrence(e, u) || !sameQuerySetOccurrence(e, e) {
		t.Fatal("SRF occurrence identity")
	}
}

// Trigger real context cancellation only after the actual entropy owner has
// been borrowed and the selection/copy loop polls it. No sleeps/large workload.
type arrayRelationalLoopContext struct {
	context.Context
	cancel context.CancelFunc
	owner  *randomState
	calls  int
}

func (c *arrayRelationalLoopContext) Err() error {
	if c.owner.mu.TryLock() {
		c.owner.mu.Unlock()
	} else {
		c.calls++
		if c.calls == 2 {
			c.cancel()
		}
	}
	return c.Context.Err()
}
func TestArrayRelationalCancellation(t *testing.T) {
	call, c := arrayRelationalUnitCall(t, "array_sample", "{1,2,3}", 2)
	elements := make([]sqltype.Value, 3*512)
	for i := range elements {
		elements[i], _ = sqltype.Integer(call.Usage.Type.Element(), int64(i))
	}
	value, err := sqltype.OwnedArrayValue(call.Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: 3}, {Lower: 1, Length: 512}}, elements)
	if err != nil {
		t.Fatal(err)
	}
	call.Args[0] = literalExpression{Info: expr.Info{Usage: call.Usage}, value: value}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boundary := &arrayRelationalLoopContext{Context: ctx, cancel: cancel, owner: &c.statementScope.op.session.arrayRandom}
	c.Context = boundary
	if result, err := call.Eval(c); !errors.Is(err, context.Canceled) || result.Type() != nil || boundary.calls < 2 {
		t.Fatal("loop cancellation published result", result, err, boundary.calls)
	}
	owner, err := c.FunctionArrayRandomOwner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Borrow(); err != nil {
		t.Fatal(err)
	}
	owner.Release()
	c.Context = context.Background()
	if result, err := call.Eval(c); err != nil || result.ArrayLen() != 1024 {
		t.Fatal("owner did not recover", result, err)
	}
	cursor, err := openFunctionCall(c, arrayRelationalUnitUnnest(t, "{1,2}"))
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.close()
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := cursor.next(canceled, call.Usage.Type.Element()); !errors.Is(err, context.Canceled) {
		t.Fatal(fmt.Sprint(err))
	}
}
