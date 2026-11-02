package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
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

var regLookupCallableCases = []struct {
	name, input     string
	kind            functions.RegLookupKind
	result          sqltype.ID
	routine, target uint32
}{
	{"to_regproc", "boolin", functions.RegLookupProc, sqltype.Regproc, 3494, 1242},
	{"to_regprocedure", "abs(integer)", functions.RegLookupProcedure, sqltype.Regprocedure, 3479, 1397},
	{"to_regoper", "||/", functions.RegLookupOper, sqltype.Regoper, 3492, 597},
	{"to_regoperator", "+(integer,integer)", functions.RegLookupOperator, sqltype.Regoperator, 3476, 551},
}

func TestRegLookupCallableBinding(t *testing.T) {
	other, _ := regLookupBind(t, "SELECT to_regrole('pg_monitor')")
	for _, tc := range regLookupCallableCases {
		t.Run(tc.name, func(t *testing.T) {
			definition, ok := functions.Lookup(tc.name)
			if !ok || !definition.ExactSpelling || !definition.SharedGates || definition.Bind == nil {
				t.Fatal("ordinary exact builtin admission", tc.name)
			}
			for _, source := range []string{"'" + tc.input + "'", "$1", "NULL", "'" + tc.input + "'::varchar"} {
				n, b := regLookupBind(t, "SELECT "+tc.name+"("+source+")")
				if n.Kind != tc.kind || n.Arg.ResultUsage() != b.Type(sqltype.Text).Usage() || n.ResultUsage() != b.Type(tc.result).Usage() || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != tc.routine || n.Validate() != nil {
					t.Fatal("exact TEXT formal, alias result and routine provenance", source, n)
				}
				if b.Input.Regproc == nil || b.Input.Operator == nil || b.Input.Regtype == nil {
					t.Fatal("coherent routine/operator/operand input authority was not captured")
				}
				for _, mutate := range []func(*functions.RegLookup){
					func(n *functions.RegLookup) { n.Kind = functions.RegLookupKind(255) },
					func(n *functions.RegLookup) { n.Arg = nil },
					func(n *functions.RegLookup) {
						n.Arg = regLookupUnitChild{Info: expr.Info{Usage: b.Type(sqltype.Bool).Usage()}}
					},
					func(n *functions.RegLookup) {
						usage := b.Type(sqltype.Text).Usage()
						usage.Typmod = 1
						n.Arg = regLookupUnitChild{Info: expr.Info{Usage: usage}}
					},
					func(n *functions.RegLookup) { n.Usage = b.Type(sqltype.Text).Usage() },
					func(n *functions.RegLookup) { n.Usage.Typmod = 1 },
					func(n *functions.RegLookup) { n.RoutineReference = nil },
					func(n *functions.RegLookup) { n.RoutineReference = other.RoutineReference },
					func(n *functions.RegLookup) { n.Property.Volatility = expr.Immutable },
				} {
					bad := n
					mutate(&bad)
					if bad.Validate() == nil || bad.ValidateStoredProvider() == nil {
						t.Fatal("malformed callable recipe admitted", bad)
					}
					if _, err := bad.Render(); err == nil {
						t.Fatal("malformed callable rendering admitted")
					}
				}
			}
			for _, source := range []string{"", "TRUE", "42", "'x'::text,'y'::text", "name => 'x'::text", "VARIADIC ARRAY['x'::text]"} {
				b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
				raw := enginetest.Statement(t, "SELECT "+tc.name+"("+source+")").(*parser.Select).Targets[0]
				_, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				enginetest.State(t, err, "42883")
			}
		})
	}
	for _, name := range []string{"To_Regproc", "To_Regprocedure", "To_Regoper", "To_Regoperator", "to_regnamespace"} {
		if _, ok := functions.Lookup(name); ok {
			t.Fatal("unrelated or wrong-case signature admitted", name)
		}
	}
}

func TestRegLookupCallableEvaluation(t *testing.T) {
	for _, tc := range regLookupCallableCases {
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
			identity := func(value sqltype.Value) uint32 {
				t.Helper()
				var id uint32
				var err error
				if sqltype.IsRoutineAlias(tc.result) {
					id, err = value.ProcedureOID()
				} else {
					id, err = value.OperatorOID()
				}
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			value, err := evaluate(tc.input)
			if err != nil || value.Type() != n.Usage.Type || value.IsNull() || identity(value) != tc.target {
				t.Fatal("real captured target identity", value, err)
			}
			soft := []string{"", " \t ", "a..b", "4294967296", "08"}
			switch tc.kind {
			case functions.RegLookupProc:
				soft = append(soft, "call_unit_missing", "abs", "pg_catalog.abs")
			case functions.RegLookupProcedure:
				soft = append(soft, "now", "abs(call_unit_missing_type)", "abs(integer,)", "now("+strings.Repeat("integer,", 100)+"integer)")
			case functions.RegLookupOper:
				soft = append(soft, "+", "pg_catalog.+", "@#@#")
			case functions.RegLookupOperator:
				soft = append(soft, "+", "+(integer)", "+(call_unit_missing_type,integer)", "+(integer,integer,integer)")
			}
			for _, input := range soft {
				value, err := evaluate(input)
				if err != nil || value.Type() != n.Usage.Type || !value.IsNull() {
					t.Fatal("producer-soft input is typed NULL", input, value, err)
				}
			}
			for _, hard := range []struct{ input, code, message, position, context string }{
				{"otherdb.public.target", "0A000", "cross-database references are not implemented: otherdb.public.target", "", ""},
				{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w", "", ""},
				{"numeric(0)", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", ""},
				{"integer(4)", "42601", `syntax error at or near "("`, "8", `invalid type name "integer(4)"`},
			} {
				input := hard.input
				if input == "numeric(0)" || input == "integer(4)" {
					switch tc.kind {
					case functions.RegLookupProcedure:
						input = "abs(" + input + ")"
					case functions.RegLookupOperator:
						input = "+(" + input + ",integer)"
					default:
						continue
					}
				} else if tc.kind == functions.RegLookupProcedure {
					input += "()"
				} else if tc.kind == functions.RegLookupOperator {
					input += "(integer,integer)"
				}
				_, err := evaluate(input)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != hard.code || diagnostic.Message != hard.message || diagnostic.Position != hard.position || diagnostic.Context != hard.context || sqltype.InputSoftError(err) {
					t.Fatal("hard producer error was changed or recovered", input, err)
				}
			}
			if tc.kind == functions.RegLookupProcedure {
				_, err := evaluate("now(" + strings.Repeat("integer,", 100) + "numeric(0))")
				enginetest.State(t, err, "22023")
				if sqltype.InputSoftError(err) {
					t.Fatal("operand101 hard modifier reclassified as soft")
				}
			}
			// Numeric and dash identities use the same genuine captured catalog
			// authority as symbolic input; the callable never bypasses setup.
			for _, numeric := range []struct {
				input string
				id    uint32
			}{{"0", 0}, {"4294967295", 4294967295}} {
				value, err := evaluate(numeric.input)
				if err != nil || value.IsNull() || identity(value) != numeric.id {
					t.Fatal("numeric identity codec", numeric, value, err)
				}
			}
			if sqltype.IsRoutineAlias(tc.result) {
				value, err := evaluate("-")
				if err != nil || value.IsNull() || identity(value) != 0 {
					t.Fatal("routine zero identity", value, err)
				}
			}
			c.input = sqltype.InputEnvironment{}
			for _, input := range []string{tc.input, "0"} {
				if _, err := evaluate(input); err == nil || sqltype.InputSoftError(err) {
					t.Fatal("missing resolver became NULL", input, err)
				}
			}
			child.value = sqltype.Null(text)
			n.Arg = child
			c.allowInput = false
			value, err = n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("strict NULL demanded input", value, err)
			}
			child.failure = sqlstate.SoftInputError(&sqlstate.Error{Code: "22P02", Message: "child input failure"})
			n.Arg = child
			if _, err := n.Eval(c); err != child.failure {
				t.Fatal("child error was recovered/replaced", err)
			}
			child.failure = nil
			n.Arg = child
			c.ctx = nil
			if _, err := n.Eval(c); err == nil {
				t.Fatal("missing context became NULL")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			c.ctx = canceled
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation became NULL", err)
			}
			// Reuse the same recipe after every failure with current authority.
			c = regLookupUnitContext{ctx: context.Background(), input: b.Input, allowInput: true}
			value, err = evaluate(tc.input)
			if err != nil || value.IsNull() || identity(value) != tc.target {
				t.Fatal("recipe recovery", value, err)
			}
		})
	}
}

func TestRegLookupCallableProtocol(t *testing.T) {
	var nodes []functions.RegLookup
	for _, tc := range regLookupCallableCases {
		n, _ := regLookupBind(t, "SELECT "+tc.name+"('"+tc.input+"')")
		nodes = append(nodes, n)
	}
	same := func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }
	for i, n := range nodes {
		t.Run(regLookupCallableCases[i].name, func(t *testing.T) {
			original := n
			if n.Properties().Volatility != expr.Stable || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || !n.LooksLikeFunction() || n.CallCost() != 1 {
				t.Fatal("stable current-authority fold contract")
			}
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !reflect.DeepEqual(rebuilt, n) || rebuilt.ResultUsage().Type != n.Usage.Type || rebuilt.(functions.RegLookup).RoutineReference != n.RoutineReference || !n.SameGroup(rebuilt, same, true) {
				t.Fatal("rebuild lost exact immutable provider ownership", err)
			}
			for j, other := range nodes {
				if i != j && (n.SameGroup(other, same, true) || other.SameGroup(n, same, false)) {
					t.Fatal("different callable kinds share a group", i, j)
				}
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || !reflect.DeepEqual(capture.Inline[0], n.Arg) || capture.Check(n, 1) != nil {
				t.Fatal("typed TEXT capture", capture, err)
			}
			rendering, err := n.Render()
			if err != nil || rendering.Tree != "RegLookup" || rendering.Name != regLookupCallableCases[i].name || rendering.Form != expr.FormCall || len(rendering.Args) != 1 || !reflect.DeepEqual(rendering.Args[0].Expr, n.Arg) || rendering.Replay.Validate() != nil || n.ValidateStoredProvider() != nil {
				t.Fatal("runtime-call rendering/replay", rendering, err)
			}
			if !n.ClosedDefault(func(expr.Expr) bool { return true }) || n.ClosedDefault(func(expr.Expr) bool { return false }) {
				t.Fatal("default closure lost TEXT operand")
			}
			replacement := regLookupUnitChild{Info: expr.Info{Usage: n.Arg.ResultUsage()}, value: sqltype.Null(n.Arg.ResultUsage().Type)}
			substituted, err := n.Substitute(n.Info, []expr.Expr{replacement})
			if err != nil || !reflect.DeepEqual(substituted.(functions.RegLookup).Arg, replacement) || substituted.(functions.RegLookup).RoutineReference != n.RoutineReference || !reflect.DeepEqual(original, n) {
				t.Fatal("substitution mutated source/provenance", err)
			}
			for _, children := range [][]expr.Expr{nil, {nil}, {n.Arg, n.Arg}} {
				if _, err := n.Rebuild(n.Info, children); err == nil {
					t.Fatal("invalid child shape accepted")
				}
			}
			// A separately bound generation has genuine same-OID routine and
			// type descriptors, not fabricated replacements or SQL output echoes.
			mapped, _ := regLookupBind(t, "SELECT "+regLookupCallableCases[i].name+"('"+regLookupCallableCases[i].input+"')")
			info := n.Info
			info.Usage, info.RoutineReference = mapped.Usage, mapped.RoutineReference
			replacement.Info.Usage = mapped.Arg.ResultUsage()
			replacement.value = sqltype.Null(replacement.Usage.Type)
			if _, err := n.Rebuild(info, []expr.Expr{replacement}); err == nil {
				t.Fatal("ordinary rebuild admitted a different provider generation")
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
			if err != nil || !changed || retagged.ResultUsage().Type != mapped.Usage.Type || retagged.(functions.RegLookup).RoutineReference != mapped.RoutineReference || retagged.(functions.RegLookup).Arg.ResultUsage() != mapped.Arg.ResultUsage() || !reflect.DeepEqual(n, original) {
				t.Fatal("authorized generation retag", err)
			}
			if _, _, err := n.Retag(info, []expr.Expr{replacement}, true, expr.Retag{Type: func(*sqltype.Descriptor) *sqltype.Descriptor { return nil }}); err == nil {
				t.Fatal("missing mapped descriptors admitted")
			}
			identity, changed, err := n.Retag(n.Info, n.Children(), false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
			if err != nil || changed || !reflect.DeepEqual(identity, n) {
				t.Fatal("identity retag changed recipe", err)
			}
		})
	}
}
