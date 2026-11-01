package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func regLookupBind(t *testing.T, query string) (functions.RegLookup, *analyze.Binder) {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := node.(functions.RegLookup)
	if !ok {
		t.Fatalf("wrong registry lookup node %T", node)
	}
	return call, b
}

type regLookupUnitChild struct {
	expr.Info
	value sqltype.Value
	failure error
}

func (n regLookupUnitChild) Eval(expr.Context) (sqltype.Value, error) {
	return n.value, n.failure
}

type regLookupUnitContext struct {
	expr.Context
	ctx context.Context
	input sqltype.InputEnvironment
	allowInput bool
}

func (c regLookupUnitContext) FunctionContext() context.Context { return c.ctx }
func (c regLookupUnitContext) FunctionInput() sqltype.InputEnvironment {
	if !c.allowInput {
		panic("strict registry lookup borrowed input authority")
	}
	return c.input
}

func TestRegLookupCollRoleBinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind functions.RegLookupKind
		result sqltype.ID
		oid uint32
	}{
		{"to_regcollation", functions.RegLookupCollation, sqltype.Regcollation, 4195},
		{"to_regrole", functions.RegLookupRole, sqltype.Regrole, 4093},
	} {
		for _, source := range []string{"'pg_monitor'", "$1", "NULL", "'pg_monitor'::varchar"} {
			n, b := regLookupBind(t, "SELECT "+tc.name+"("+source+")")
			if n.Kind != tc.kind || n.Arg.ResultUsage().Type != b.Type(sqltype.Text) || n.ResultUsage() != b.Type(tc.result).Usage() || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != tc.oid || n.Validate() != nil {
				t.Fatal("fixed TEXT formal/result/provenance", tc, source, n)
			}
			if b.Input.Regcollation == nil || b.Input.Regrole == nil {
				t.Fatal("destination input authority was not captured")
			}
		}
		n, b := regLookupBind(t, "SELECT "+tc.name+"('pg_monitor')")
		for _, mutate := range []func(*functions.RegLookup){
			func(n *functions.RegLookup) { n.Kind = functions.RegLookupKind(2) },
			func(n *functions.RegLookup) { n.Arg = nil },
			func(n *functions.RegLookup) { n.Arg = regLookupUnitChild{Info: expr.Info{Usage: b.Type(sqltype.Bool).Usage()}} },
			func(n *functions.RegLookup) { n.Usage = b.Type(sqltype.Text).Usage() },
			func(n *functions.RegLookup) { n.Usage.Typmod = 1 },
			func(n *functions.RegLookup) { n.RoutineReference = nil },
			func(n *functions.RegLookup) { n.Property.Volatility = expr.Immutable },
		} {
			bad := n
			mutate(&bad)
			if bad.Validate() == nil {
				t.Fatal("invalid registry recipe admitted", bad)
			}
		}
		for _, source := range []string{"TRUE", "42", "", "'x'::text,'y'::text", "name => 'x'::text"} {
			binder := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, "SELECT "+tc.name+"("+source+")").(*parser.Select).Targets[0]
			_, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			enginetest.State(t, err, "42883")
		}
	}
	for _, name := range []string{"to_regcollation", "to_regrole"} {
		definition, ok := functions.Lookup(name)
		if !ok || !definition.ExactSpelling || !definition.SharedGates || definition.Bind == nil {
			t.Fatal("ordinary exact registry admission", name)
		}
	}
	for _, name := range []string{"To_Regrole", "To_Regcollation", "to_regnamespace"} {
		if _, ok := functions.Lookup(name); ok {
			t.Fatal("lookup widened beyond the two exact callable spellings", name)
		}
	}
}

func TestRegLookupCollRoleEvaluation(t *testing.T) {
	for _, tc := range []struct {
		query, valid string
		identity uint32
	}{
		{`SELECT to_regcollation('pg_catalog."C"')`, `pg_catalog."C"`, 950},
		{`SELECT to_regrole('pg_monitor')`, "pg_monitor", 3373},
	} {
		n, b := regLookupBind(t, tc.query)
		text := n.Arg.ResultUsage().Type
		child := regLookupUnitChild{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, tc.valid)}
		n.Arg = child
		c := regLookupUnitContext{ctx: context.Background(), input: b.Input, allowInput: true}
		value, err := n.Eval(c)
		if err != nil || value.Type() != n.Usage.Type || value.IsNull() {
			t.Fatal("real captured catalog input", value, err)
		}
		identity := func(value sqltype.Value) uint32 {
			t.Helper()
			var id uint32
			var err error
			if n.Kind == functions.RegLookupRole {
				id, err = value.RoleOID()
			} else {
				id, err = value.ObjectOID()
			}
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		if identity(value) != tc.identity {
			t.Fatal("released object identity", value)
		}
		for _, input := range []string{"cl_unit_absent", "", " \t ", ".s", "s.", "s..t", `"s`, `"s"tail`, "08", "4294967296"} {
			child.value = sqltype.String(text, input)
			n.Arg = child
			value, err := n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("producer-soft conversion must be typed NULL", input, value, err)
			}
		}
		if n.Kind == functions.RegLookupRole {
			child.value = sqltype.String(text, "x.y.z.w")
			n.Arg = child
			value, err := n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("role qualification is soft", value, err)
			}
		} else {
			for _, hard := range []struct{ input, code, message string }{
				{"otherdb.public.c", "0A000", "cross-database references are not implemented: otherdb.public.c"},
				{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w"},
			} {
				child.value = sqltype.String(text, hard.input)
				n.Arg = child
				_, err := n.Eval(c)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != hard.code || diagnostic.Message != hard.message || sqltype.InputSoftError(err) {
					t.Fatal("hard conversion was suppressed/reclassified", hard, err)
				}
			}
		}
		// Numeric and dash input must use the complete codec without demanding a
		// symbolic resolver. In particular, 010 is the OID codec's octal 8.
		c.input = sqltype.InputEnvironment{}
		for _, numeric := range []struct{ text string; id uint32 }{{"0", 0}, {"-", 0}, {"010", 8}, {"4294967295", 4294967295}} {
			child.value = sqltype.String(text, numeric.text)
			n.Arg = child
			value, err := n.Eval(c)
			if err != nil || value.IsNull() || value.Type() != n.Usage.Type || identity(value) != numeric.id {
				t.Fatal("complete numeric/dash codec", numeric, value, err)
			}
		}
		child.value = sqltype.String(text, tc.valid)
		n.Arg = child
		if _, err := n.Eval(c); err == nil || sqltype.InputSoftError(err) {
			t.Fatal("missing resolver setup must remain hard", err)
		}
		child.value = sqltype.Null(text)
		n.Arg = child
		c.allowInput = false
		value, err = n.Eval(c)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("strict typed NULL borrowed authority", value, err)
		}
		child.failure = sqlstate.SoftInputError(&sqlstate.Error{Code: "22P02", Message: "child input failure"})
		n.Arg = child
		if _, err := n.Eval(c); err != child.failure {
			t.Fatal("child soft-looking error was swallowed", err)
		}
		child.failure = nil
		n.Arg = child
		c.ctx = nil
		if _, err := n.Eval(c); err == nil {
			t.Fatal("missing execution context became NULL")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		c.ctx = canceled
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation became NULL", err)
		}
	}
}

func TestRegLookupCollRoleProtocol(t *testing.T) {
	collation, _ := regLookupBind(t, `SELECT to_regcollation('pg_catalog."C"')`)
	role, _ := regLookupBind(t, `SELECT to_regrole('pg_monitor')`)
	same := func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }
	for _, n := range []functions.RegLookup{collation, role} {
		if n.Properties().Volatility != expr.Stable || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || !n.LooksLikeFunction() || n.CallCost() != 1 {
			t.Fatal("stable runtime fold policy", n)
		}
		original := n
		rebuilt, err := n.Rebuild(n.Info, n.Children())
		if err != nil || !reflect.DeepEqual(n, rebuilt) || rebuilt.ResultUsage().Type != n.Usage.Type || rebuilt.(functions.RegLookup).RoutineReference != n.RoutineReference {
			t.Fatal("reconstruction lost exact descriptors/provenance", err)
		}
		if !n.SameGroup(rebuilt, same, true) || collation.SameGroup(role, same, true) || role.SameGroup(collation, same, true) {
			t.Fatal("group identity lost lookup target")
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || !reflect.DeepEqual(capture.Inline[0], n.Arg) || capture.Check(n, 1) != nil {
			t.Fatal("capture lost inline child", capture, err)
		}
		rendering, err := n.Render()
		name := "to_regcollation"
		if n.Kind == functions.RegLookupRole {
			name = "to_regrole"
		}
		if err != nil || rendering.Form != expr.FormCall || rendering.Name != name || len(rendering.Args) != 1 || !reflect.DeepEqual(rendering.Args[0].Expr, n.Arg) || rendering.Replay.Validate() != nil || n.ValidateStoredProvider() != nil {
			t.Fatal("callable stored rendering", rendering, err)
		}
		if !n.ClosedDefault(func(expr.Expr) bool { return true }) || n.ClosedDefault(func(expr.Expr) bool { return false }) {
			t.Fatal("default closure lost operand")
		}
		replacement := regLookupUnitChild{Info: expr.Info{Usage: n.Arg.ResultUsage()}, value: sqltype.Null(n.Arg.ResultUsage().Type)}
		substituted, err := n.Substitute(n.Info, []expr.Expr{replacement})
		if err != nil || !reflect.DeepEqual(substituted.(functions.RegLookup).Arg, replacement) || substituted.(functions.RegLookup).RoutineReference != n.RoutineReference || !reflect.DeepEqual(n, original) {
			t.Fatal("substitution mutated immutable source/provenance", err)
		}
		for _, children := range [][]expr.Expr{nil, {nil}, {n.Arg, n.Arg}} {
			if _, err := n.Rebuild(n.Info, children); err == nil {
				t.Fatal("invalid child shape admitted")
			}
		}
		mapped := map[sqltype.ID]*sqltype.Descriptor{}
		for _, descriptor := range sqltype.Builtins() {
			mapped[descriptor.ID()] = descriptor
		}
		info := n.Info
		info.Usage = mapped[n.Usage.Type.ID()].Usage()
		replacement.Info.Usage = mapped[sqltype.Text].Usage()
		replacement.value = sqltype.Null(mapped[sqltype.Text])
		if _, err := n.Rebuild(info, []expr.Expr{replacement}); err == nil {
			t.Fatal("ordinary reconstruction silently retagged provider generations")
		}
		retagged, changed, err := n.Retag(info, []expr.Expr{replacement}, true, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return mapped[d.ID()] }})
		if err != nil || !changed || retagged.ResultUsage().Type != mapped[n.Usage.Type.ID()] || retagged.(functions.RegLookup).Arg.ResultUsage().Type != mapped[sqltype.Text] || retagged.(functions.RegLookup).RoutineReference != n.RoutineReference || !reflect.DeepEqual(n, original) {
			t.Fatal("authorized retag lost exact mapped descriptors/provenance", err)
		}
		identity, changed, err := n.Retag(n.Info, n.Children(), false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
		if err != nil || changed || !reflect.DeepEqual(identity, n) {
			t.Fatal("identity mapping changed immutable recipe", err)
		}
	}
}
