package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var regLookupRelTypeCases = []struct {
	name, input string
	result      sqltype.ID
	routine     uint32
	value       int64
}{
	{"to_regclass", "pg_catalog.pg_class", sqltype.Regclass, 3495, 1259},
	{"to_regnamespace", "pg_catalog", sqltype.Regnamespace, 4086, 11},
	{"to_regtype", "integer", sqltype.Regtype, 3493, 23},
	{"to_regtypemod", "varchar(12)", sqltype.Int4, 6317, 16},
}

type regLookupRelTypeCountingChild struct {
	regLookupUnitChild
	calls *int
}

func (n regLookupRelTypeCountingChild) Eval(c expr.Context) (sqltype.Value, error) {
	*n.calls = *n.calls + 1
	return n.regLookupUnitChild.Eval(c)
}

func TestRegLookupRelTypeBinding(t *testing.T) {
	for _, tc := range regLookupRelTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			definition, ok := functions.Lookup(tc.name)
			if !ok || !definition.ExactSpelling || !definition.SharedGates || definition.Bind == nil {
				t.Fatal("missing ordinary builtin declaration", tc.name)
			}
			for _, source := range []string{"'" + tc.input + "'", "$1", "NULL", "'" + tc.input + "'::varchar"} {
				n, b := regLookupBind(t, "SELECT "+tc.name+"("+source+")")
				if n.Arg.ResultUsage() != b.Type(sqltype.Text).Usage() || n.ResultUsage() != b.Type(tc.result).Usage() || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != tc.routine || n.Validate() != nil {
					t.Fatal("TEXT formal, exact descriptor and routine provenance", source, n)
				}
				if b.Input.Regtype == nil {
					t.Fatal("binding failed to capture declaration input, including INT4 typmod result")
				}
				for _, mutate := range []func(*functions.RegLookup){
					func(n *functions.RegLookup) { n.Kind = functions.RegLookupKind(255) },
					func(n *functions.RegLookup) { n.Arg = nil },
					func(n *functions.RegLookup) {
						n.Arg = regLookupUnitChild{Info: expr.Info{Usage: b.Type(sqltype.Bool).Usage()}}
					},
					func(n *functions.RegLookup) { n.Usage = b.Type(sqltype.Text).Usage() },
					func(n *functions.RegLookup) { n.Usage.Typmod = 1 },
					func(n *functions.RegLookup) { n.RoutineReference = nil },
					func(n *functions.RegLookup) { n.Property.Volatility = expr.Immutable },
				} {
					bad := n
					mutate(&bad)
					if bad.Validate() == nil || bad.ValidateStoredProvider() == nil {
						t.Fatal("invalid immutable recipe admitted", bad)
					}
				}
			}
			for _, source := range []string{"", "TRUE", "42", "'x'::text,'y'::text", "name => 'x'::text"} {
				b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
				raw := enginetest.Statement(t, "SELECT "+tc.name+"("+source+")").(*parser.Select).Targets[0]
				_, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				enginetest.State(t, err, "42883")
			}
		})
	}
}

func regLookupRelTypeUnitValue(t *testing.T, value sqltype.Value) int64 {
	t.Helper()
	var id uint32
	var err error
	switch value.Type().ID() {
	case sqltype.Regclass:
		id, err = value.ClassOID()
	case sqltype.Regnamespace:
		id, err = value.ObjectOID()
	case sqltype.Regtype:
		id, err = value.TypeOID()
	case sqltype.Int4:
		v, e := value.Integer()
		if e != nil {
			t.Fatal(e)
		}
		return v
	default:
		t.Fatal("unexpected typed result", value.Type())
	}
	if err != nil {
		t.Fatal(err)
	}
	return int64(id)
}

func TestRegLookupRelTypeEvaluation(t *testing.T) {
	for _, tc := range regLookupRelTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			n, b := regLookupBind(t, "SELECT "+tc.name+"('"+tc.input+"')")
			text := n.Arg.ResultUsage().Type
			child := regLookupUnitChild{Info: expr.Info{Usage: text.Usage()}}
			c := regLookupUnitContext{ctx: context.Background(), input: b.Input, allowInput: true}
			evaluate := func(input string) (sqltype.Value, error) {
				child.value = sqltype.String(text, input)
				n.Arg = child
				return n.Eval(c)
			}
			check := func(input string, want int64) {
				t.Helper()
				v, err := evaluate(input)
				if err != nil || v.IsNull() || v.Type() != n.Usage.Type {
					t.Fatal("real destination conversion", input, v, err)
				}
				if got := regLookupRelTypeUnitValue(t, v); got != want {
					t.Fatal("independent typed result", input, got, want)
				}
			}
			check(tc.input, tc.value)
			calls := 0
			n.Arg = regLookupRelTypeCountingChild{regLookupUnitChild: child, calls: &calls}
			counted, countErr := n.Eval(c)
			if countErr != nil || counted.IsNull() || regLookupRelTypeUnitValue(t, counted) != tc.value || calls != 1 {
				t.Fatal("child must evaluate exactly once with real typed result", counted, countErr, calls)
			}
			for _, input := range []string{"rl_unit_missing", "", " \t "} {
				v, err := evaluate(input)
				if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
					t.Fatal("direct producer-soft failure must be typed NULL", input, v, err)
				}
			}
			if tc.result == sqltype.Int4 || tc.result == sqltype.Regtype {
				_, err := evaluate("varchar(0)")
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "length for type varchar must be at least 1" || sqltype.InputSoftError(err) {
					t.Fatal("hard provider error was suppressed", err)
				}
			} else if tc.result == sqltype.Regclass {
				_, err := evaluate("a.b.c.d")
				enginetest.State(t, err, "42601")
				if sqltype.InputSoftError(err) {
					t.Fatal("hard qualification became soft", err)
				}
			}
			if tc.result == sqltype.Int4 {
				for _, modifier := range []struct {
					input string
					want  int64
				}{{"numeric(7,4)[]", 458760}, {"char", 5}, {"bit", 1}, {"pg_catalog.bit", -1}} {
					check(modifier.input, modifier.want)
				}
			} else {
				for _, numeric := range []struct {
					input string
					want  int64
				}{{"0", 0}, {"-", 0}, {"010", 8}, {"4294967295", 4294967295}} {
					check(numeric.input, numeric.want)
				}
			}
			// Extraction failures precede destination authority as well.
			child.value = sqltype.Boolean(b.Type(sqltype.Bool), true)
			n.Arg, c.allowInput = child, false
			if _, err := n.Eval(c); err == nil || sqltype.InputSoftError(err) {
				t.Fatal("non-TEXT child became destination NULL", err)
			}
			c.allowInput = true
			// Changing the current context must affect this very recipe, not a
			// newly bound replacement. No previous authority is retained.
			c.input = sqltype.InputEnvironment{}
			if _, err := evaluate(tc.input); err == nil || sqltype.InputSoftError(err) {
				t.Fatal("missing current authority became NULL", err)
			}
			child.value = sqltype.Null(text)
			n.Arg, c.allowInput = child, false
			v, err := n.Eval(c)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("strict NULL borrowed destination input", v, err)
			}
			child.failure = sqlstate.SoftInputError(&sqlstate.Error{Code: "22P02", Message: "child input failure"})
			n.Arg = child
			if _, err := n.Eval(c); err != child.failure {
				t.Fatal("child soft-looking error was recovered", err)
			}
			child.failure = nil
			n.Arg, c.ctx = child, nil
			if _, err := n.Eval(c); err == nil {
				t.Fatal("missing execution context became NULL")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.ctx = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation became NULL", err)
			}
			c = regLookupUnitContext{ctx: context.Background(), input: b.Input, allowInput: true}
			check(tc.input, tc.value)
		})
	}
}

func TestRegLookupRelTypeProtocol(t *testing.T) {
	var nodes []functions.RegLookup
	for _, tc := range regLookupRelTypeCases {
		n, _ := regLookupBind(t, "SELECT "+tc.name+"('"+tc.input+"')")
		nodes = append(nodes, n)
	}
	same := func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }
	for i, n := range nodes {
		t.Run(regLookupRelTypeCases[i].name, func(t *testing.T) {
			original := n
			if n.Properties().Volatility != expr.Stable || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || !n.LooksLikeFunction() || n.CallCost() != 1 {
				t.Fatal("stable per-execution recipe contract")
			}
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !reflect.DeepEqual(n, rebuilt) || !n.SameGroup(rebuilt, same, true) {
				t.Fatal("exact rebuild", err)
			}
			for j, other := range nodes {
				if i != j && (n.SameGroup(other, same, true) || other.SameGroup(n, same, false)) {
					t.Fatal("different lookup targets share a group")
				}
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || !reflect.DeepEqual(capture.Inline[0], n.Arg) || capture.Check(n, 1) != nil {
				t.Fatal("one typed child capture", capture, err)
			}
			rendering, err := n.Render()
			if err != nil || rendering.Name != regLookupRelTypeCases[i].name || rendering.Form != expr.FormCall || rendering.Replay.Validate() != nil || n.ValidateStoredProvider() != nil {
				t.Fatal("stored runtime rendering", rendering, err)
			}
			if !n.ClosedDefault(func(expr.Expr) bool { return true }) || n.ClosedDefault(func(expr.Expr) bool { return false }) {
				t.Fatal("default closure lost child")
			}
			replacement := regLookupUnitChild{Info: expr.Info{Usage: n.Arg.ResultUsage()}, value: sqltype.Null(n.Arg.ResultUsage().Type)}
			substituted, err := n.Substitute(n.Info, []expr.Expr{replacement})
			if err != nil || !reflect.DeepEqual(substituted.(functions.RegLookup).Arg, replacement) || substituted.(functions.RegLookup).RoutineReference != n.RoutineReference || !reflect.DeepEqual(n, original) {
				t.Fatal("substitution mutated source/provenance", err)
			}
			for _, children := range [][]expr.Expr{nil, {nil}, {n.Arg, n.Arg}} {
				if _, err := n.Rebuild(n.Info, children); err == nil {
					t.Fatal("invalid children admitted")
				}
			}
			mapped, _ := regLookupBind(t, "SELECT "+regLookupRelTypeCases[i].name+"('"+regLookupRelTypeCases[i].input+"')")
			info := n.Info
			info.Usage, info.RoutineReference = mapped.Usage, mapped.RoutineReference
			replacement.Info.Usage = mapped.Arg.ResultUsage()
			replacement.value = sqltype.Null(replacement.Usage.Type)
			if _, err := n.Rebuild(info, []expr.Expr{replacement}); err == nil {
				t.Fatal("ordinary rebuild retagged provider generation")
			}
			env := expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor {
				if d.ID() == sqltype.Text {
					return mapped.Arg.ResultUsage().Type
				}
				if d.ID() == n.Usage.Type.ID() {
					return mapped.Usage.Type
				}
				return nil
			}}
			retagged, changed, err := n.Retag(info, []expr.Expr{replacement}, true, env)
			if err != nil || !changed || retagged.ResultUsage() != mapped.Usage || retagged.(functions.RegLookup).RoutineReference != mapped.RoutineReference || !reflect.DeepEqual(n, original) {
				t.Fatal("authorized retag lost immutable ownership", err)
			}
		})
	}
}

// Hiding declaration parsing preserves the smaller REGTYPE I/O contract.
type regLookupIdentityOnly struct{ sqltype.RegtypeResolver }

type regLookupUsageProbe struct {
	sqltype.RegtypeResolver
	usage   sqltype.RegtypeUsageResolver
	calls   *int
	failure error
}

func (p regLookupUsageProbe) ResolveInputUsage(ctx context.Context, text string) (sqltype.Usage, error) {
	*p.calls = *p.calls + 1
	if p.failure != nil {
		return sqltype.Usage{}, p.failure
	}
	return p.usage.ResolveInputUsage(ctx, text)
}

func TestRegLookupRelTypeDeclarationCapability(t *testing.T) {
	n, b := regLookupBind(t, "SELECT to_regtypemod('varchar(12)')")
	resolver, ok := b.Input.Regtype.(sqltype.RegtypeUsageResolver)
	if !ok {
		t.Fatal("real captured type input lacks declaration capability")
	}
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: b.Input}}
	c.Input.Regtype = regLookupIdentityOnly{b.Input.Regtype}
	if _, err := n.Eval(c); err == nil || err.Error() != "stuntgres: registry typmod lookup requires declaration input authority" || sqltype.InputSoftError(err) {
		t.Fatal("missing declaration authority must be a hard setup error", err)
	} else if _, sql := err.(interface{ SQLState() string }); sql {
		t.Fatal("missing internal authority acquired an SQLSTATE", err)
	}
	identity, _ := regLookupBind(t, "SELECT to_regtype('varchar(12)')")
	value, err := identity.Eval(c)
	if err != nil || value.IsNull() || regLookupRelTypeUnitValue(t, value) != int64(sqltype.VarChar) {
		t.Fatal("optional capability changed the existing identity resolver contract", value, err)
	}
	calls := 0
	probe := regLookupUsageProbe{RegtypeResolver: b.Input.Regtype, usage: resolver, calls: &calls}
	c.Input.Regtype = probe
	value, err = n.Eval(c)
	if err != nil || value.Type() != n.Usage.Type || value.IsNull() || regLookupRelTypeUnitValue(t, value) != 16 || calls != 1 {
		t.Fatal("declaration destination must resolve once to the genuine encoded modifier", value, err, calls)
	}
	diagnostic := &sqlstate.Error{Code: "42704", Message: "destination probe"}
	for _, failure := range []error{
		sqlstate.SoftInputError(diagnostic),
		sqlstate.HardInputError(diagnostic),
		errors.New("ordinary destination failure"),
		context.Canceled,
	} {
		probe.failure = failure
		c.Input.Regtype = probe
		before := calls
		value, err := n.Eval(c)
		if calls != before+1 {
			t.Fatal("destination invoked more than once")
		}
		if sqltype.InputSoftError(failure) {
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("direct soft destination must be typed NULL", value, err)
			}
		} else if err != failure {
			t.Fatal("hard, ordinary or cancellation destination error was replaced", err)
		}
	}
}
