package views_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The constructor obtains its own canonical input and runs real typed
// preparation. Altered output/source combinations cannot replace that result.
func TestPreparedAcceptedDefaultSourceDerivation(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		t.Run(language, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			declaration := "RETURN $1+1"
			if language == "pl" {
				declaration = "LANGUAGE plpgsql AS $$BEGIN RETURN $1+1; END$$"
			}
			for _, q := range []string{
				"CREATE FUNCTION recipe_arg(integer) RETURNS integer " + declaration,
				"CREATE FUNCTION recipe_hidden(integer,integer DEFAULT recipe_arg(9)) RETURNS integer RETURN $1+$2",
				"CREATE FUNCTION recipe_named(a integer,b integer DEFAULT recipe_arg(9)) RETURNS integer RETURN a+b",
				"CREATE FUNCTION recipe_variadic(VARIADIC a integer[]) RETURNS integer RETURN a[1]",
				"CREATE TABLE recipe_default_rows(v integer DEFAULT recipe_arg(4),nested integer DEFAULT recipe_arg(recipe_arg(4)),siblings integer DEFAULT recipe_arg(4)+recipe_arg(9),hidden integer DEFAULT recipe_hidden(4),folded integer DEFAULT recipe_arg(1+3),pruned integer DEFAULT CASE WHEN false THEN recipe_arg(9) WHEN true THEN recipe_arg(4) ELSE recipe_arg(999) END,literal integer DEFAULT 1+3,named integer DEFAULT recipe_named(b=>recipe_arg(9),a=>4),variadic_value integer DEFAULT recipe_variadic(4,9),searched integer DEFAULT CASE WHEN true THEN recipe_arg(4) END,simple integer DEFAULT CASE 1 WHEN 1 THEN recipe_arg(4) ELSE recipe_arg(9) END,otherwise integer DEFAULT CASE WHEN false THEN recipe_arg(9) ELSE recipe_arg(4) END,coalesced integer DEFAULT COALESCE(NULL::integer,recipe_arg(4)))",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			read := e.CatalogRead()
			ref := enginetest.CatalogRef(t, e, read, "recipe_default_rows")
			relation := read.MustRelation(ref.Address)
			roots := make([]expr.Expr, len(relation.Attributes))
			for i := range roots {
				roots[i], _ = analyze.ColumnDefault(read, relation, i)
			}
			var siblings []plan.SQLRoutineExpr
			plan.WalkExpression(roots[2], func(node expr.Expr) {
				if call, ok := node.(plan.SQLRoutineExpr); ok {
					siblings = append(siblings, call)
				}
			})
			if len(siblings) != 2 {
				t.Fatal("expected two lexical siblings")
			}
			otherArgument := siblings[1].Args[0]
			mapRoot := func(node expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) expr.Expr {
				next, _, err := stored.MapExpression(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, leaf)
				if err != nil {
					t.Fatal(err)
				}
				return next
			}
			alterArgument := func(node expr.Expr) expr.Expr {
				call := node.(plan.SQLRoutineExpr)
				call.Args = slices.Clone(call.Args)
				arg := call.Args[0].(plan.LiteralExpr)
				arg.Value = otherArgument.(plan.LiteralExpr).Value
				call.Args[0] = arg
				return call
			}
			for _, tc := range []struct {
				name      string
				slot      int
				allow     bool
				transform func(expr.Expr) expr.Expr
			}{
				{"original", 0, true, nil}, {"nested-original", 1, true, nil}, {"siblings-original", 2, true, nil}, {"hidden-original", 3, true, nil},
				{"sharing-preserving-COW", 0, true, nil}, {"genuine-argument-folding", 4, true, nil}, {"genuine-CASE-arm-pruning", 5, true, nil}, {"genuine-derived-literal", 6, true, nil}, {"named-original", 7, true, nil}, {"variadic-original", 8, true, nil},
				{"searched-CASE-null-slots", 9, true, nil}, {"simple-CASE-pruned-arm", 10, true, nil}, {"searched-CASE-pruned-otherwise", 11, true, nil},
				{"coalesce-retains-runtime-carrier", 12, true, nil},
				{"unrelated-root", 0, false, func(expr.Expr) expr.Expr { return otherArgument }},
				{"altered-argument", 0, false, alterArgument},
				{"altered-nested-argument", 1, false, func(node expr.Expr) expr.Expr {
					call := node.(plan.SQLRoutineExpr)
					call.Args = slices.Clone(call.Args)
					call.Args[0] = alterArgument(call.Args[0])
					return call
				}},
				{"shifted-sibling-path", 2, false, func(node expr.Expr) expr.Expr {
					index := 0
					return mapRoot(node, func(child expr.Expr) (expr.Expr, bool, error) {
						if _, ok := child.(plan.SQLRoutineExpr); ok {
							next := siblings[1-index]
							index++
							return next, true, nil
						}
						return child, false, nil
					})
				}},
				{"recombined-source-root", 2, false, func(expr.Expr) expr.Expr { return siblings[0] }},
				{"altered-hidden-default", 3, false, func(node expr.Expr) expr.Expr {
					call := node.(plan.SQLRoutineExpr)
					call.Args = slices.Clone(call.Args)
					hidden := call.Args[1].(plan.SQLRoutineExpr)
					hidden.Args = slices.Clone(hidden.Args)
					hidden.Args[0] = roots[0].(plan.SQLRoutineExpr).Args[0]
					call.Args[1] = hidden
					return call
				}},
				{"altered-named-payload", 7, false, func(node expr.Expr) expr.Expr {
					call := node.(plan.SQLRoutineExpr)
					call.Args = slices.Clone(call.Args)
					call.Args[1] = otherArgument
					return call
				}},
				{"altered-variadic-payload", 8, false, alterArgument},
				{"removed-pruned-arm", 5, false, func(node expr.Expr) expr.Expr { return node.(plan.CaseExpr).Arms[0].Result }},
				{"borrowed-pruned-root", 5, false, func(expr.Expr) expr.Expr { return roots[0] }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					source, origin := analyze.ColumnDefault(read, relation, tc.slot)
					var before []plan.LiteralExpr
					plan.WalkExpression(source, func(n expr.Expr) {
						if literal, ok := n.(plan.LiteralExpr); ok {
							before = append(before, literal)
						}
					})
					owner := exec.NewAcceptedDefaultSet()
					key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO recipe_default_rows DEFAULT VALUES"), Slot: tc.slot}
					ordinary, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[tc.slot])
					if err != nil {
						t.Fatal(err)
					}
					if err := owner.Seal(41); err != nil {
						t.Fatal(err)
					}
					artifact, err := owner.Lookup(key, ref, relation.Attributes[tc.slot])
					if err != nil || artifact == nil {
						t.Fatal("missing authentic prepared result", err)
					}
					origin.Accepted, origin.Occurrence = artifact, key
					node := artifact.Expression()
					if tc.transform != nil {
						node = tc.transform(source)
					}
					err = admitAcceptedDefault(t, e, artifact, key, origin, node, "postgres", nil)
					if tc.allow && err != nil {
						t.Fatal("genuine source derivation rejected", err)
					}
					if !tc.allow && err == nil {
						t.Fatal("unproved prepared/source recombination admitted")
					}
					if tc.name == "sharing-preserving-COW" {
						original := source.(plan.SQLRoutineExpr)
						prepared := ordinary.(plan.SQLRoutineExpr)
						if &original.Args[0] != &prepared.Args[0] {
							t.Fatal("unchanged argument backing copied")
						}
					}
					if tc.name == "genuine-argument-folding" {
						if _, ok := ordinary.(plan.SQLRoutineExpr).Args[0].(plan.LiteralExpr); !ok {
							t.Fatal("fixture did not derive literal argument")
						}
					}
					if tc.name == "genuine-CASE-arm-pruning" {
						original, sourceCase := source.(plan.CaseExpr)
						prepared, preparedCase := ordinary.(plan.CaseExpr)
						if !sourceCase || !preparedCase || original.Test != nil || len(original.Arms) != 2 || prepared.Test != nil || len(prepared.Arms) != 0 {
							t.Fatal("fixture did not prune the original CASE arms to its decisive result")
						}
						selected := original.Arms[1].Result.(plan.SQLRoutineExpr)
						call, ok := prepared.Otherwise.(plan.SQLRoutineExpr)
						if !ok || call.Ref != selected.Ref || call.Procedure != selected.Procedure || &call.Args[0] != &selected.Args[0] {
							t.Fatal("pruning lost the selected source call or copied unchanged argument backing")
						}
						calls := reflect.ValueOf(artifact).Elem().FieldByName("calls")
						if calls.Len() != 1 {
							t.Fatal("pruning retained a removed arm's call", calls.Len())
						}
						path := calls.Index(0).Elem().FieldByName("path")
						if path.Len() != 1 || path.Index(0).Int() != 7 {
							t.Fatal("surviving call adopted its final Otherwise slot instead of original arm path", path)
						}
						owned := node.(expr.Composite).Children()[0].(plan.CaseExpr)
						sealed, wrapped := owned.Otherwise.(expr.Composite)
						if owned.Test != nil || len(owned.Arms) != 0 || !wrapped || len(sealed.Children()) != len(selected.Args) {
							t.Fatal("sealed result did not retain the genuinely pruned CASE")
						}
						survivor := reflect.ValueOf(owned.Otherwise)
						if survivor.Kind() != reflect.Pointer || survivor.Pointer() != calls.Index(0).Pointer() || &sealed.Children()[0] != &selected.Args[0] {
							t.Fatal("sealed pruned output lost the exact surviving occurrence or argument backing")
						}
					}
					if tc.name == "genuine-derived-literal" {
						if _, ok := ordinary.(plan.LiteralExpr); !ok {
							t.Fatal("fixture did not derive literal root")
						}
					}
					if tc.slot >= 9 && tc.slot <= 11 {
						calls := reflect.ValueOf(artifact).Elem().FieldByName("calls")
						if calls.Len() != 1 {
							t.Fatal("pruned CASE retained an unrelated call occurrence", calls.Len())
						}
						path := calls.Index(0).Elem().FieldByName("path")
						want := int64(4) // Test, Otherwise, arm.Test, arm.Condition, arm.Result.
						if tc.slot == 11 {
							want = 1
						}
						if path.Len() != 1 || path.Index(0).Int() != want {
							t.Fatal("CASE lost its original mapper slot, including nil slots", path)
						}
					}
					if tc.name == "coalesce-retains-runtime-carrier" {
						original := source.(plan.ConditionalExpr)
						prepared, ok := ordinary.(plan.ConditionalExpr)
						if !ok || prepared.Kind != original.Kind || len(original.Args) != 2 || len(prepared.Args) != 1 {
							t.Fatal("COALESCE did not preserve its incumbent single-runtime-argument carrier")
						}
						call, ok := prepared.Args[0].(plan.SQLRoutineExpr)
						selected := original.Args[1].(plan.SQLRoutineExpr)
						if !ok || &call.Args[0] != &selected.Args[0] {
							t.Fatal("COALESCE changed the surviving source call's immutable argument backing")
						}
						calls := reflect.ValueOf(artifact).Elem().FieldByName("calls")
						if calls.Len() != 1 {
							t.Fatal("COALESCE lost its independent surviving call")
						}
						path := calls.Index(0).Elem().FieldByName("path")
						if path.Len() != 1 || path.Index(0).Int() != 1 {
							t.Fatal("COALESCE survivor lost its original source slot", path)
						}
					}
					var after []plan.LiteralExpr
					plan.WalkExpression(source, func(n expr.Expr) {
						if literal, ok := n.(plan.LiteralExpr); ok {
							after = append(after, literal)
						}
					})
					if !reflect.DeepEqual(before, after) {
						t.Fatal("preparation or negative mutation changed source payload")
					}
				})
			}
			// Exact immutable source pointer/revision ownership is checked before any
			// preparation, not inferred from an equal default or a genuine foreign result.
			for _, kind := range []string{"copied-source", "wrong-attribute", "copied-owner", "sealed-owner"} {
				t.Run(kind, func(t *testing.T) {
					owner := exec.NewAcceptedDefaultSet()
					selected, attribute := relation, relation.Attributes[0]
					switch kind {
					case "copied-source":
						copy := *relation
						selected = &copy
					case "wrong-attribute":
						attribute = -1
					case "copied-owner":
						copy := *owner
						owner = &copy
					case "sealed-owner":
						if err := owner.Seal(41); err != nil {
							t.Fatal(err)
						}
					}
					key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO recipe_default_rows DEFAULT VALUES")}
					if _, _, err := prepareAcceptedDefault(t, e, owner, key, selected, ref, attribute); err == nil {
						t.Fatal("unowned/incomplete source preparation admitted")
					}
				})
			}
		})
	}
}
