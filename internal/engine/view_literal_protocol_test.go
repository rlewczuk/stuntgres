package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These named nodes deliberately are not the concrete nodes in the old capture
// switch. Only their embedded, actual functions nodes may supply capture facts.
// Rendering, rebuilding and evaluation are not alternate capture protocols.
type captureJSONBSet struct{ functions.JSONBSet }

func (captureJSONBSet) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated JSONBSet") }
func (captureJSONBSet) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt JSONBSet")
}
func (captureJSONBSet) Render() (expr.Rendering, error) { panic("capture rendered JSONBSet") }
func (captureJSONBSet) Children() []expr.Expr           { panic("capture used executable JSONBSet defaults") }

type captureNullCount struct{ functions.NullCount }

func (captureNullCount) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated NullCount")
}
func (captureNullCount) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt NullCount")
}
func (captureNullCount) Render() (expr.Rendering, error) { panic("capture rendered NullCount") }

type captureStringVariadic struct{ functions.StringVariadic }

func (captureStringVariadic) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated variadic call")
}
func (captureStringVariadic) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt variadic call")
}
func (captureStringVariadic) Render() (expr.Rendering, error) {
	panic("capture rendered variadic call")
}

type captureArraySearch struct{ functions.ArraySearch }

func (captureArraySearch) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated ArraySearch")
}
func (captureArraySearch) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt ArraySearch")
}
func (captureArraySearch) Render() (expr.Rendering, error) { panic("capture rendered ArraySearch") }

type captureSleep struct{ functions.DatetimeSleep }

func (captureSleep) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated sleep") }
func (captureSleep) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt sleep")
}
func (captureSleep) Render() (expr.Rendering, error) { panic("capture rendered sleep") }

type capturePosix struct{ functions.PosixSubstring }

func (capturePosix) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated POSIX substring")
}
func (capturePosix) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt POSIX substring")
}
func (capturePosix) Render() (expr.Rendering, error) { panic("capture rendered POSIX substring") }

type captureSimilar struct{ functions.SimilarSubstring }

func (captureSimilar) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated SIMILAR substring")
}
func (captureSimilar) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt SIMILAR substring")
}
func (captureSimilar) Render() (expr.Rendering, error) { panic("capture rendered SIMILAR substring") }

type captureSQLJSON struct{ functions.SQLJSON }

func (captureSQLJSON) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated SQLJSON") }
func (captureSQLJSON) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt SQLJSON")
}
func (captureSQLJSON) Render() (expr.Rendering, error) { panic("capture rendered SQLJSON") }

type captureTimezone struct{ functions.Timezone }

func (captureTimezone) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated timezone") }
func (captureTimezone) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt timezone")
}
func (captureTimezone) Render() (expr.Rendering, error) { panic("capture rendered timezone") }

type captureVersion struct{ functions.VersionInformation }

func (captureVersion) Eval(expr.Context) (sqltype.Value, error) { panic("capture evaluated version") }
func (captureVersion) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt version")
}
func (captureVersion) Render() (expr.Rendering, error) { panic("capture rendered version") }

// Composite alone must not admit arbitrary raw syntax, even with matching arity.
type captureCompositeOnly struct {
	expr.Expr
	args []expr.Expr
}

func (e captureCompositeOnly) Children() []expr.Expr { return e.args }
func (captureCompositeOnly) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	panic("capture rebuilt Composite-only node")
}
func (captureCompositeOnly) Eval(expr.Context) (sqltype.Value, error) {
	panic("capture evaluated Composite-only node")
}

func protocolBinder(t *testing.T) *analyze.Binder {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	column := catalog.Column{Name: "k", Usage: b.Type(sqltype.Text).Usage()}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: []analyze.QueryColumn{{Descriptor: column, Slot: 0, Leaf: -1, Physical: -1}}, Frame: []catalog.Column{column}}
	return b
}

func protocolBind(t *testing.T, b *analyze.Binder, query string) (parser.Expr, expr.Expr) {
	t.Helper()
	raw := statement(t, query).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(query, err)
	}
	return raw, node
}

type protocolLiteral struct {
	id   sqltype.ID
	text string
}

func protocolLiterals(t *testing.T, b *analyze.Binder, want map[analyze.ViewLiteralPath]protocolLiteral) {
	t.Helper()
	if len(b.Capture.Literals) != len(want) {
		t.Fatalf("literal key set: got %v, want %v", b.Capture.Literals, want)
	}
	for key, expected := range want {
		got, ok := b.Capture.Literals[key]
		if !ok {
			t.Fatalf("missing literal at %+v", key)
		}
		text, err := got.Value.Type().Output(context.Background(), got.Value)
		if err != nil || got.ResultUsage().Type.ID() != expected.id || text != expected.text {
			t.Fatalf("literal at %+v: usage=%v text=%q err=%v, want %+v", key, got.ResultUsage(), text, err, expected)
		}
	}
}

func TestViewLiteralProtocolWrittenOperands(t *testing.T) {
	t.Run("named_defaults_and_column", func(t *testing.T) {
		b := protocolBinder(t)
		raw, node := protocolBind(t, b, `SELECT jsonb_set_lax(replacement => '9', path => ARRAY[k], jsonb_in => '{}')`)
		n := node.(functions.JSONBSet)
		before := n
		if n.WrittenCount != 3 || n.Count != 5 || n.WrittenSlots[0] != 2 || n.WrittenSlots[1] != 1 || n.WrittenSlots[2] != 0 {
			t.Fatal("fixture lost named/default boundary", n)
		}
		// Executable defaults are distinct poison operands, not raw occurrences.
		n.Args[3] = captureCompositeOnly{Expr: n.Args[3]}
		n.Args[4] = captureCompositeOnly{Expr: n.Args[4]}
		frozen := n
		if err := b.CaptureViewLiterals(raw, captureJSONBSet{n}, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		base := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
		first, last, column := base, base, base
		first.Node, column.Node, last.Node = 1, 3, 4
		protocolLiterals(t, b, map[analyze.ViewLiteralPath]protocolLiteral{first: {sqltype.JSONB, "9"}, last: {sqltype.JSONB, "{}"}})
		if !reflect.DeepEqual(b.Capture.Columns, map[analyze.ViewLiteralPath]analyze.ViewColumn{column: {Frame: b.FrameID(), Slot: 0}}) {
			t.Fatal("written column association", b.Capture.Columns)
		}
		if len(b.Capture.CastUsages) != 0 || !reflect.DeepEqual(n, frozen) || !reflect.DeepEqual(before.WrittenSlots, n.WrittenSlots) || before.Labels != n.Labels {
			t.Fatal("capture mutated inputs or captured defaults", b.Capture.CastUsages)
		}
	})
	t.Run("variadic_last_operand", func(t *testing.T) {
		b := protocolBinder(t)
		raw, node := protocolBind(t, b, `SELECT concat(VARIADIC ARRAY[1,2])`)
		n := node.(functions.StringVariadic)
		before := append([]expr.Expr(nil), n.Args...)
		if !n.Variadic {
			t.Fatal("fixture lost variadic operand")
		}
		if err := b.CaptureViewLiterals(raw, captureStringVariadic{n}, analyze.ViewTargetLiteral, 2); err != nil {
			t.Fatal(err)
		}
		one := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral, Target: 2, Node: 2}
		two := one
		two.Node = 3
		protocolLiterals(t, b, map[analyze.ViewLiteralPath]protocolLiteral{one: {sqltype.Int4, "1"}, two: {sqltype.Int4, "2"}})
		if !reflect.DeepEqual(before, n.Args) || len(b.Capture.Columns) != 0 || len(b.Capture.CastUsages) != 0 {
			t.Fatal("variadic operand storage changed")
		}
	})
}

func TestViewLiteralProtocolOccurrenceOwnership(t *testing.T) {
	b := protocolBinder(t)
	raw, node := protocolBind(t, b, `SELECT num_nulls(num_nulls(11,22),33)`)
	outer := node.(functions.NullCount)
	args := append([]expr.Expr(nil), outer.Args...)
	inner := outer.Args[0].(functions.NullCount)
	inner.Args = append([]expr.Expr(nil), inner.Args...)
	outer.Args = append([]expr.Expr(nil), outer.Args...)
	outer.Args[0] = captureNullCount{inner}
	// All leaf source positions deliberately coincide. Structural occurrence
	// ownership, not parser offsets or formal slots, must distinguish them.
	for i, child := range inner.Args {
		lit := child.(plan.LiteralExpr)
		lit.Pos = parser.Position{Offset: 7}
		inner.Args[i] = lit
	}
	outer.Args[0] = captureNullCount{inner}
	last := outer.Args[1].(plan.LiteralExpr)
	last.Pos = parser.Position{Offset: 7}
	outer.Args[1] = last
	contexts := []analyze.ViewLiteralPath{
		{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral, Target: 0},
		{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral, Target: 1},
		{Query: b.FrameID(), Clause: analyze.ViewWhereLiteral},
		{Query: b.FrameID() + 1, Clause: analyze.ViewTargetLiteral},
		{Query: b.FrameID(), Clause: analyze.ViewFunctionLiteral, Source: 1, Target: 1},
		{Query: b.FrameID(), Clause: analyze.ViewValuesCellLiteral, Row: 1, Target: 1},
	}
	want := make(map[analyze.ViewLiteralPath]protocolLiteral)
	for _, path := range contexts {
		ordinal := 0
		if err := b.CaptureViewLiteralTree(raw, captureNullCount{outer}, path, &ordinal); err != nil {
			t.Fatal(err)
		}
		if ordinal != 5 {
			t.Fatal("non-written/default node consumed ordinal", ordinal)
		}
		for i, text := range []string{"11", "22", "33"} {
			key := path
			key.Node = i + 2
			want[key] = protocolLiteral{sqltype.Int4, text}
		}
	}
	protocolLiterals(t, b, want)
	if !reflect.DeepEqual(args, node.(functions.NullCount).Args) || len(b.Capture.Columns) != 0 || len(b.Capture.CastUsages) != 0 {
		t.Fatal("occurrence capture changed input or annotation ownership")
	}
}

func TestViewLiteralProtocolAnnotations(t *testing.T) {
	t.Run("selected_array_argument", func(t *testing.T) {
		b := protocolBinder(t)
		raw, node := protocolBind(t, b, `SELECT array_position(ARRAY[1::smallint],2)`)
		n := node.(functions.ArraySearch)
		before := append([]expr.Expr(nil), n.Args...)
		if err := b.CaptureViewLiterals(raw, captureArraySearch{n}, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		parent := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral, Argument: 1}
		written := parent
		written.Argument = 0
		written.Node = 2
		want := map[analyze.ViewLiteralPath]sqltype.Usage{parent: n.Args[0].ResultUsage(), written: b.Type(sqltype.Int2).Usage()}
		if !reflect.DeepEqual(b.Capture.CastUsages, want) || !reflect.DeepEqual(before, n.Args) {
			t.Fatal("selected parent versus written cast ownership", b.Capture.CastUsages, want)
		}
		one, two := parent, parent
		one.Argument, two.Argument = 0, 0
		one.Node, two.Node = 3, 4
		protocolLiterals(t, b, map[analyze.ViewLiteralPath]protocolLiteral{one: {sqltype.Int4, "1"}, two: {sqltype.Int4, "2"}})
	})
	t.Run("sleep_call_not_argument", func(t *testing.T) {
		b := protocolBinder(t)
		raw, node := protocolBind(t, b, `SELECT pg_sleep(17)`)
		n := node.(functions.DatetimeSleep)
		before := n
		if err := b.CaptureViewLiterals(raw, captureSleep{n}, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		parent := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
		if !reflect.DeepEqual(b.Capture.CastUsages, map[analyze.ViewLiteralPath]sqltype.Usage{parent: b.Type(sqltype.Float8).Usage()}) || !reflect.DeepEqual(before, n) {
			t.Fatal("sleep call-level usage", b.Capture.CastUsages)
		}
		child := parent
		child.Node = 1
		protocolLiterals(t, b, map[analyze.ViewLiteralPath]protocolLiteral{child: {sqltype.Int4, "17"}})
	})
	for _, tc := range []struct {
		name, query string
		similar     bool
	}{
		{"posix", `SELECT substring('abc','b')`, false},
		{"similar", `SELECT substring('abc' SIMILAR '%#"b#"%' ESCAPE '#')`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := protocolBinder(t)
			raw, node := protocolBind(t, b, tc.query)
			var wrapped expr.Expr
			if tc.similar {
				wrapped = captureSimilar{node.(functions.SimilarSubstring)}
			} else {
				wrapped = capturePosix{node.(functions.PosixSubstring)}
			}
			if err := b.CaptureViewLiterals(raw, wrapped, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			parent := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			want := map[analyze.ViewLiteralPath]bool{parent: true}
			if tc.similar {
				if !reflect.DeepEqual(b.Capture.SimilarSubstrings, want) || len(b.Capture.PosixSubstrings) != 0 {
					t.Fatal("SIMILAR annotation ownership")
				}
			} else if !reflect.DeepEqual(b.Capture.PosixSubstrings, want) || len(b.Capture.SimilarSubstrings) != 0 {
				t.Fatal("POSIX annotation ownership")
			}
			if len(b.Capture.CastUsages) != 0 {
				t.Fatal("substring acquired cast annotations", b.Capture.CastUsages)
			}
		})
	}
	t.Run("formatted_json_returning", func(t *testing.T) {
		b := protocolBinder(t)
		raw, node := protocolBind(t, b, `SELECT JSON_ARRAY(k FORMAT JSON RETURNING text)`)
		conversion := node.(plan.CoercionExpr)
		n := conversion.Child.(functions.SQLJSON)
		before := append([]expr.Expr(nil), n.Args...)
		conversion.Child = captureSQLJSON{n}
		if err := b.CaptureViewLiterals(raw, conversion, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		parent := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
		child := parent
		child.Node = 1
		if !reflect.DeepEqual(b.Capture.CastUsages, map[analyze.ViewLiteralPath]sqltype.Usage{parent: b.Type(sqltype.Text).Usage()}) || !reflect.DeepEqual(b.Capture.Columns, map[analyze.ViewLiteralPath]analyze.ViewColumn{child: {Frame: b.FrameID(), Slot: 0}}) || len(b.Capture.Literals) != 0 || !reflect.DeepEqual(before, n.Args) {
			t.Fatal("FORMAT input or RETURNING ownership", b.Capture.CastUsages, b.Capture.Columns)
		}
	})
	t.Run("dot_builtin", func(t *testing.T) {
		b := protocolBinder(t)
		_, node := protocolBind(t, b, `SELECT num_nulls(k)`)
		n := node.(functions.NullCount)
		raw := statement(t, `SELECT (k).num_nulls`).(*parser.Select).Targets[0]
		if err := b.CaptureViewLiterals(raw, captureNullCount{n}, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		parent := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
		child := parent
		child.Node = 1
		want := analyze.CompositeOccurrence{Kind: analyze.CompositeDotBuiltin, Base: b.Type(sqltype.Text).Usage(), Function: functions.CountNulls}
		if !reflect.DeepEqual(b.Capture.Composites, map[analyze.ViewLiteralPath]analyze.CompositeOccurrence{parent: want}) || !reflect.DeepEqual(b.Capture.Columns, map[analyze.ViewLiteralPath]analyze.ViewColumn{child: {Frame: b.FrameID(), Slot: 0}}) || len(b.Capture.CastUsages) != 0 {
			t.Fatal("dot builtin identity/base ownership", b.Capture.Composites, b.Capture.Columns)
		}
	})
}

func TestViewLiteralProtocolRejectsMismatch(t *testing.T) {
	b := protocolBinder(t)
	raw, node := protocolBind(t, b, `SELECT num_nulls(1,2)`)
	n := node.(functions.NullCount)
	cases := []struct {
		name string
		raw  parser.Expr
		node expr.Expr
	}{
		{"composite_is_not_admission", raw, captureCompositeOnly{Expr: n, args: n.Args}},
		{"wrong_raw_kind", statement(t, `SELECT 1+2`).(*parser.Select).Targets[0], captureNullCount{n}},
		{"missing_argument", statement(t, `SELECT num_nulls(1)`).(*parser.Select).Targets[0], captureNullCount{n}},
		{"extra_argument", statement(t, `SELECT num_nulls(1,2,3)`).(*parser.Select).Targets[0], captureNullCount{n}},
		{"field_arity", statement(t, `SELECT (k).num_nulls`).(*parser.Select).Targets[0], captureNullCount{n}},
	}
	zoneRaw, zone := protocolBind(t, b, `SELECT TIMESTAMP '2000-01-02' AT TIME ZONE 'UTC'`)
	z := zone.(functions.Timezone)
	z.Local = true
	cases = append(cases, struct {
		name string
		raw  parser.Expr
		node expr.Expr
	}{"timezone_local_mismatch", zoneRaw, captureTimezone{z}})
	versionRaw, version := protocolBind(t, b, `SELECT version()`)
	v := version.(functions.VersionInformation)
	v.Property = expr.Properties{}
	cases = append(cases, struct {
		name string
		raw  parser.Expr
		node expr.Expr
	}{"invalid_leaf_properties", versionRaw, captureVersion{v}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := protocolBinder(t)
			err := fresh.CaptureViewLiterals(tc.raw, tc.node, analyze.ViewTargetLiteral, 0)
			if err == nil {
				t.Fatal("mismatched raw/bound pair accepted")
			}
			if tc.name == "composite_is_not_admission" && !strings.Contains(err.Error(), "mismatched") {
				t.Fatal(err)
			}
		})
	}
}

// Exercise the actual bindSelect/function-FROM capture callers, not fabricated
// path.query/source values. The subquery owns another raw preorder.
func TestViewLiteralProtocolQueryBoundaries(t *testing.T) {
	b := protocolBinder(t)
	b.Params.NoParameters = true
	raw := statement(t, `SELECT (SELECT num_nulls(33,44)) FROM generate_series(1,num_nulls(11,22))`).(*parser.Select)
	query, err := b.BindSelect(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[analyze.ViewLiteralPath]protocolLiteral{
		{Query: 1, Clause: analyze.ViewFunctionLiteral, Node: 1}: {sqltype.Int4, "1"},
		{Query: 1, Clause: analyze.ViewFunctionLiteral, Node: 3}: {sqltype.Int4, "11"},
		{Query: 1, Clause: analyze.ViewFunctionLiteral, Node: 4}: {sqltype.Int4, "22"},
		{Query: 2, Clause: analyze.ViewTargetLiteral, Node: 1}:   {sqltype.Int4, "33"},
		{Query: 2, Clause: analyze.ViewTargetLiteral, Node: 2}:   {sqltype.Int4, "44"},
	}
	protocolLiterals(t, b, want)
	frames, queries := analyze.CollectQueryFrames(query.Root)
	definition := &viewDefinition{syntax: raw, query: query, frames: frames, queries: queries, namespaces: b.Capture.Namespaces, literals: b.Capture.Literals, columns: b.Capture.Columns, castUsages: b.Capture.CastUsages}
	if err := definition.validateLiterals(b.BindingCatalog()); err != nil {
		t.Fatal(err)
	}
	fresh := jsonObjectUnitTypes()
	remapped, err := remapFrozenPresentation(b.BindingCatalog(), definition, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(remapped.Literals()) != len(want) || !reflect.DeepEqual(remapped.columns, definition.columns) {
		t.Fatal("remap changed occurrence identity")
	}
	for path, original := range definition.Literals() {
		next := remapped.Literals()[path]
		if next.Usage.Type != fresh[original.Usage.Type.ID()] || next.Usage.Type == original.Usage.Type {
			t.Fatal("retag lost fresh descriptor", path)
		}
		if original.Usage.Type != b.Capture.Literals[path].Usage.Type {
			t.Fatal("retag mutated captured source")
		}
	}
	protocolLiterals(t, b, want)
}

func TestViewLiteralProtocolInstalledWrapper(t *testing.T) {
	b := protocolBinder(t)
	raw, node := protocolBind(t, b, `SELECT uuidv4()`)
	inner := node.(functions.UUIDGeneration)
	ref := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 7}, Epoch: 1}
	procedure := &catalog.Procedure{}
	wrapper := plan.InstalledUUIDOsspExpr{UUIDGeneration: inner, Procedure: procedure, Ref: ref}
	if _, ok := any(wrapper).(expr.Capturable); !ok {
		t.Fatal("fixture lost promoted capture hook")
	}
	if err := b.CaptureViewLiterals(raw, wrapper, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	mapped, err := remapBoundExpression(wrapper, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := mapped.(plan.InstalledUUIDOsspExpr)
	if !ok || got.Ref != ref || got.Procedure != procedure {
		t.Fatal("capture/remap erased installed identity", mapped)
	}
	// This deliberately non-installed identity must still fail validation.
	// A cutover to the promoted bare UUID node would wrongly lose this guard.
	if err := validateInstalledUUIDOssp(got, b.BindingCatalog()); err == nil {
		t.Fatal("lost installed identity validation")
	}
	if len(b.Capture.Literals) != 0 || len(b.Capture.CastUsages) != 0 {
		t.Fatal("leaf acquired captures")
	}
}
