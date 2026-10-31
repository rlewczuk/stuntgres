package enginetests_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A genuine checked stage owns callable declarations separately from its bound
// frame/slot expression. Equal lexical calls cannot substitute for those owners.
func TestPGRewriteQuerySemantics(t *testing.T) {
	for _, window := range []bool{false, true} {
		name := "aggregate"
		if window {
			name = "window"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA rw_sem_a", "CREATE SCHEMA rw_sem_b", "CREATE TABLE public.rw_sem_input(v integer)", "CREATE AGGREGATE rw_sem_a.pick(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE rw_sem_b.pick(integer)(SFUNC=pg_catalog.int4larger,STYPE=integer,INITCOND='0')"} {
				enginetest.SessionSQL(t, session, q)
			}
			query := "SELECT pick(v) AS total FROM public.rw_sem_input"
			if window {
				query = "SELECT v,pick(v) OVER() AS total FROM public.rw_sem_input ORDER BY v"
			}
			type captured struct {
				owner      *catalog.Relation
				definition *stored.View
				routine    ids.ObjectRef
				procedure  *catalog.Procedure
				value      sqltype.Value
				syntax     string
			}
			var owners [2]captured
			for i, schema := range []string{"rw_sem_a", "rw_sem_b"} {
				enginetest.SessionSQL(t, session, "SET search_path TO "+schema+",public,pg_catalog")
				viewName := []string{"rw_sem_first", "rw_sem_second"}[i]
				enginetest.SessionSQL(t, session, "CREATE VIEW public."+viewName+" AS "+query)
				read := e.CatalogRead()
				owner := read.MustRelation(enginetest.CatalogRef(t, e, read, viewName).Address)
				definition := stored.ViewOf(owner)
				target := 0
				if window {
					target = 1
				}
				frozen, ok := definition.Syntax().Targets[target].(parser.AggregateExpr)
				if !ok || !frozen.StoredAggregate || frozen.Schema == nil || frozen.Schema.Text != schema {
					t.Fatal("actual selected namespace was not retained in frozen syntax", frozen)
				}
				var routine ids.ObjectRef
				var procedure *catalog.Procedure
				count := 0
				var visit func(plan.Node)
				visit = func(node plan.Node) {
					if node == nil {
						return
					}
					if window {
						if stage, ok := node.(*plan.WindowNode); ok {
							assertRewriteWindowDependencies(t, read, stage, true)
							for _, call := range stage.Calls {
								count++
								routine, procedure = call.Routine, call.Procedure
								if call.Expression == nil || call.Expression.Call != 0 {
									t.Fatal("lost window occurrence association", call)
								}
							}
						}
					} else if stage, ok := node.(*plan.AggregateNode); ok {
						for _, call := range stage.Calls {
							count++
							routine, procedure = call.Routine, call.Procedure
						}
					}
					left, right := plan.QueryInputs(node)
					visit(left)
					visit(right)
				}
				visit(definition.Query().Root)
				if count != 1 || routine.Address.ObjectID == 0 || procedure == nil || routine.Address != procedure.Address() || read.Get(routine.Address) != procedure {
					t.Fatal("lost actual selected declaration", count, routine, procedure)
				}
				typ, _ := read.LookupType(sqltype.PgNodeTree)
				_, value, err := stored.RewriteTrees(t.Context(), read, owner, typ, nil, definition)
				if err != nil {
					t.Fatal(err)
				}
				var syntax strings.Builder
				if err := parser.AppendStructuralSyntax(t.Context(), &syntax, definition.Syntax()); err != nil {
					t.Fatal(err)
				}
				owners[i] = captured{owner: owner, definition: definition, routine: routine, procedure: procedure, value: value, syntax: syntax.String()}
			}
			if owners[0].syntax == owners[1].syntax {
				t.Fatal("distinct selected namespaces disappeared from genuine frozen syntax")
			}
			if owners[0].routine.Address == owners[1].routine.Address || owners[0].procedure == owners[1].procedure {
				t.Fatal("fixture did not select distinct immutable declarations")
			}
			var output [2]string
			for i, owner := range owners {
				var err error
				output[i], err = owner.value.Type().Output(t.Context(), owner.value)
				if err != nil {
					t.Fatal(err)
				}
				if window && !strings.Contains(output[i], "CachedOrder slot") {
					t.Fatal("actual outer-sort cache-read association missing", output[i])
				}
				id := strconv.FormatUint(uint64(owner.routine.Address.ObjectID), 10)
				if !regexp.MustCompile(`(?:^|[[:space:]/])` + id + `(?:[[:space:]/}]|$)`).MatchString(output[i]) {
					t.Fatal("owned action tree omitted selected declaration", owner.routine, output[i])
				}
			}
			if output[0] == output[1] {
				t.Fatal("different selected stage owners collapsed", output)
			}
			enginetest.SessionSQL(t, session, "CREATE OR REPLACE VIEW public.rw_sem_first AS "+query)
			current := e.CatalogRead()
			replacement := current.MustRelation(owners[0].owner.Address())
			if replacement.View.RuleOID() != owners[0].definition.RuleOID() {
				t.Fatal("replacement changed rule identity")
			}
			enginetest.SessionSQL(t, session, "DROP AGGREGATE rw_sem_a.pick(integer) RESTRICT")
			if owners[0].owner.View != owners[0].definition {
				t.Fatal("replacement mutated captured definition")
			}
			carrier, err := owners[0].value.Tree()
			if err != nil {
				t.Fatal(err)
			}
			if got, err := carrier.StructuralOutput(t.Context()); err != nil || got != output[0] {
				t.Fatal("retained stage re-resolved declaration", got, err)
			}
		})
	}
	t.Run("builtin_window", rewriteBuiltinWindowSemantics)
}

func rewriteBuiltinWindowSemantics(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE rw_sem_builtin_input(v integer)")
	var selected [2]ids.ObjectAddress
	var operations [2]plan.WindowOperation
	for i, name := range []string{"row_number", "rank"} {
		viewName := "rw_sem_builtin_" + name
		enginetest.SessionSQL(t, session, "CREATE VIEW "+viewName+" AS SELECT "+name+"() OVER(ORDER BY v) AS result FROM rw_sem_builtin_input")
		read := e.CatalogRead()
		owner := read.MustRelation(enginetest.CatalogRef(t, e, read, viewName).Address)
		definition := stored.ViewOf(owner)
		var call *plan.WindowCall
		var visit func(plan.Node)
		visit = func(node plan.Node) {
			if node == nil {
				return
			}
			if stage, ok := node.(*plan.WindowNode); ok {
				if len(stage.Calls) != 1 || call != nil {
					t.Fatal("unexpected builtin window owners", stage.Calls)
				}
				assertRewriteWindowDependencies(t, read, stage, false)
				call = &stage.Calls[0]
			}
			left, right := plan.QueryInputs(node)
			visit(left)
			visit(right)
		}
		visit(definition.Query().Root)
		if call == nil || call.Procedure == nil || call.Expression == nil || call.Routine.Address != call.Procedure.Address() || read.Get(call.Routine.Address) != call.Procedure {
			t.Fatal("missing genuine builtin window declaration", call)
		}
		selected[i], operations[i] = call.Routine.Address, call.Operation
		typ, _ := read.LookupType(sqltype.PgNodeTree)
		_, action, err := stored.RewriteTrees(t.Context(), read, owner, typ, nil, definition)
		if err != nil {
			t.Fatal(err)
		}
		text, err := action.Type().Output(t.Context(), action)
		if err != nil {
			t.Fatal(err)
		}
		id := strconv.FormatUint(uint64(call.Routine.Address.ObjectID), 10)
		if !regexp.MustCompile(`(?:^|[[:space:]/])` + id + `(?:[[:space:]/}]|$)`).MatchString(text) {
			t.Fatal("builtin window selected identity missing", name, call.Routine, text)
		}
	}
	if selected[0] == selected[1] || operations[0] == operations[1] {
		t.Fatal("distinct builtin window owners collapsed", selected, operations)
	}
}

func assertRewriteWindowDependencies(t *testing.T, read catalog.Read, stage *plan.WindowNode, installed bool) {
	t.Helper()
	if len(stage.Calls) != 1 {
		t.Fatal("expected one genuine selected window call")
	}
	copy := *stage
	call := stage.Calls[0]
	call.AnalysisOnly = true
	copy.Calls = []plan.WindowCall{call, call}
	refs := stored.DependRefs{Context: t.Context(), Catalog: read}
	refs.Query(&copy)
	addresses, err := refs.Finish()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, address := range addresses {
		if address == call.Routine.Address {
			count++
		}
	}
	want := 0
	if installed {
		want = 1
	}
	if count != want {
		t.Fatal("selected window dependency pin/dedupe/analysis-only projection", count, want, addresses)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceled := stored.DependRefs{Context: ctx, Catalog: read}
	canceled.Query(&copy)
	if _, err := canceled.Finish(); !errors.Is(err, context.Canceled) {
		t.Fatal("dependency cancellation lost", err)
	}
	bad := *stage
	bad.Calls = append([]plan.WindowCall(nil), stage.Calls...)
	bad.Calls[0].Routine.Address.ObjectID++
	invalid := stored.DependRefs{Context: t.Context(), Catalog: read}
	invalid.Query(&bad)
	if _, err := invalid.Finish(); err == nil {
		t.Fatal("mismatched selected declaration accepted")
	}
}
