package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGStatSubscriptionStatsRoutineAuthority(t *testing.T) {
	e, other := engine.New(), engine.New()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6231}
	view := e.CatalogRead()
	p, ok := view.Procedure(address)
	q, exists := other.CatalogRead().Procedure(address)
	if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(6231) {
		t.Fatal("owned descriptors/canonical declaration", p, q)
	}
	if p.Builtin.Identity != (catalog.RoutineIdentity{ID: 6231, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_subscription_stats", Signature: "26"}) || p.Signature != "26" || p.RoutineKind != catalog.FunctionRoutine || p.Result != 2249 || !p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.ReturnsSet() || p.RowEstimate() != 0 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 {
		t.Fatal("exact released noncallable declaration", p, p.Builtin)
	}
	if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_subscription_stats"}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil || p.InputNames != nil {
		t.Fatal("default PUBLIC/internal declaration", p)
	}
	outputs := p.OutputDeclaration()
	if outputs == nil || outputs != q.OutputDeclaration() || outputs.Len() != 11 || outputs.Inputs() != 1 || outputs.Name(0) != "subid" {
		t.Fatal("immutable IN/OUT signature", outputs)
	}
	for i, name := range [...]string{"subid", "apply_error_count", "sync_error_count", "confl_insert_exists", "confl_update_origin_differs", "confl_update_exists", "confl_update_missing", "confl_delete_origin_differs", "confl_delete_missing", "confl_multiple_unique_conflicts", "stats_reset"} {
		if outputs.Name(i+1) != name || outputs.Type(i) != ([...]ids.OID{26, 20, 20, 20, 20, 20, 20, 20, 20, 20, 1184})[i] {
			t.Fatal("OUT field", i, name)
		}
	}
	if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
		if _, err := routine.LookupBuiltinRoutineDeclaration(provider, p.Name(), []sqltype.ID{sqltype.Oid}); err == nil {
			t.Fatal("declaration granted callable getter", provider)
		}
	}
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/retained"}[retained], func(t *testing.T) {
				e := engine.New()
				admin := enginetest.StartSession(t, e, nil)
				s := enginetest.StartSession(t, e, nil)
				key := resetStatsCreate(t, e, admin, "stats_authority")
				fact := subscriptionStatsSeed(e, key, subscriptionStatsLocal)
				statsFunctionExec(t, admin, "CREATE ROLE stats_authority_reader")
				statsFunctionExec(t, s, "SET ROLE stats_authority_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, s, "ROLLBACK")
				ast, d := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription_stats WHERE subname=$1")
				name := "stats_authority"
				conjunction, bound := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription_stats WHERE subname=$1 AND $2")
				executeConjunction := func(argument *bool) (core.Result, error) {
					t.Helper()
					op := enginetest.TransactionOperation(t, s)
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						return core.Result{}, err
					}
					scope.Expected = &bound
					result, err := scope.Execute(conjunction, 2, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
						if info.Count != 2 || info.Types[0].Type.ID() != sqltype.Name || info.Types[1].Type.ID() != sqltype.Bool {
							t.Fatal("canonical NAME/BOOL frame", info)
						}
						value, err := info.Types[0].Type.InputWithEnvironment(ctx, name, -1, input)
						frame[0] = value
						frame[1] = sqltype.Null(info.Types[1].Type)
						if argument != nil {
							frame[1] = sqltype.Boolean(info.Types[1].Type, *argument)
						}
						return err
					})
					return result, scope.Finish(err)
				}
				no, yes := false, true
				owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
				if !owned.IsZero() {
					t.Fatal("Describe configured Reader")
				}
				capture := func(denied bool) {
					t.Helper()
					op := enginetest.TransactionOperation(t, s)
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := scope.Finish(nil); err != nil {
							t.Error(err)
						}
					}()
					view := e.CatalogRead()
					source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSubscriptionStatsOID})
					ref := view.Owner().Domain.Ref(view.Epoch(), source.Address())
					e.Mu.Lock()
					rows, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, source, nil)
					if denied {
						if rows != nil {
							rows.Close()
							t.Fatal("denied getter published source")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_subscription_stats"})
						return
					}
					if err != nil || rows == nil {
						t.Fatal("admitted captured source", err)
					}
					defer rows.Close()
					if len(rows.Anonymous) != 1 {
						t.Fatal("missing admitted identity")
					}
					if n, err := rows.Anonymous[0][2].Integer(); err != nil || n != 11 {
						t.Fatal("positive retained fact", n, err)
					}
				}
				if retained {
					capture(false)
					subscriptionStatsSeed(e, key, subscriptionStatsPeer)
				}
				before := bgwriterReaderState(s)
				providerGeneration, providerFact := e.Statistics.Generation(), e.Statistics.Read(key)
				assertUnchanged := func() {
					t.Helper()
					if bgwriterReaderState(s) != before || e.Statistics.Generation() != providerGeneration || e.Statistics.Read(key) != providerFact {
						t.Fatal("undemanded/denied execution changed Reader owner, generation, backing or provider")
					}
					if retained {
						cache := owned.FieldByName("cache")
						got := cache.MapIndex(reflect.ValueOf(key))
						if !got.IsValid() || !got.FieldByName("exists").Bool() || !got.FieldByName("fact").Equal(reflect.ValueOf(fact)) {
							t.Fatal("original owning-key positive cut was not retained", got, fact)
						}
					}
				}
				statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription_stats(oid) FROM PUBLIC")
				capture(true)
				if bgwriterReaderState(s) != before {
					t.Fatal("denied source configured/cleared/replaced retained Reader")
				}
				statsFunctionExec(t, s, "SAVEPOINT stats_name_getter_denial")
				denied, err := subscriptionStatsNameExecute(t, e, s, ast, &d, &name)
				if denied.Rows != nil {
					denied.Rows.Close()
					t.Fatal("denied name-filtered held getter published rows")
				}
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_subscription_stats"})
				statsFunctionExec(t, s, "ROLLBACK TO stats_name_getter_denial")
				if bgwriterReaderState(s) != before {
					t.Fatal("denied name-filtered held getter changed retained Reader")
				}
				for _, query := range []string{"SELECT * FROM pg_stat_subscription_stats WHERE FALSE", "SELECT * FROM pg_stat_subscription_stats WHERE NULL::boolean"} {
					statement, description := enginetest.PreparedDescription(t, e, s, query)
					result, err := enginetest.PreparedExecute(e, s, statement, &description)
					if err != nil {
						t.Fatal("unused getter demanded EXECUTE", err)
					}
					subscriptionStatsOwned(t, result, nil)
					assertUnchanged()
					if bgwriterReaderState(s) != before {
						t.Fatal("constant-rejected source changed Reader ownership")
					}
				}
				for _, predicate := range []string{"FALSE", "NULL::boolean"} {
					statement, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription_stats WHERE subname=$1 AND "+predicate)
					result, err := subscriptionStatsNameExecute(t, e, s, statement, &description, &name)
					if err != nil {
						t.Fatal("unused name-filtered getter demanded EXECUTE", err)
					}
					subscriptionStatsOwned(t, result, nil)
					assertUnchanged()
					if bgwriterReaderState(s) != before {
						t.Fatal("unused name-filtered source changed retained Reader")
					}
				}
				// These executions inspect actual Reader ownership, not just public
				// emptiness. One accepted definition imports a fresh BOOL each time.
				for _, argument := range []*bool{&no, nil, &no} {
					result, err := executeConjunction(argument)
					if err != nil {
						t.Fatal("current rejected conjunction demanded getter", err)
					}
					subscriptionStatsOwned(t, result, nil)
					assertUnchanged()
					statsFunctionExec(t, s, "SAVEPOINT stats_conjunction_getter_denial")
					denied, err := executeConjunction(&yes)
					if denied.Rows != nil {
						denied.Rows.Close()
						t.Fatal("denied TRUE conjunction published partial rows")
					}
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_subscription_stats"})
					statsFunctionExec(t, s, "ROLLBACK TO stats_conjunction_getter_denial")
					assertUnchanged()
				}
				owner, epoch := bound.CatalogOwner, bound.CatalogEpoch
				generation := bound.PlanGeneration
				results, parameters := &bound.Results[0], &bound.Parameters.Types[0]
				if owner != e.CatalogRead().Owner() || epoch != e.CatalogRead().Epoch() || bound.PlanOwner != s.SessionToken || generation != s.DiscardPlansGeneration {
					t.Fatal("conjunction description lost its real definition owner/generation")
				}
				statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription_stats(oid) TO PUBLIC")
				result, err := subscriptionStatsNameExecute(t, e, s, ast, &d, &name)
				if err != nil {
					t.Fatal(err)
				}
				if retained {
					subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: fact})
				} else {
					subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: e.Statistics.Read(key)})
				}
				if retained && bgwriterReaderState(s) != before {
					t.Fatal("regrant refreshed retained positive fact/time/backing")
				}
				result, err = executeConjunction(&yes)
				if err != nil {
					t.Fatal("same conjunction after getter regrant", err)
				}
				wantFact := providerFact
				if retained {
					wantFact = fact
				}
				subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: wantFact})
				if bound.CatalogOwner != owner || bound.CatalogEpoch != epoch || bound.PlanGeneration != generation || &bound.Results[0] != results || &bound.Parameters.Types[0] != parameters {
					t.Fatal("current BOOL/ACL execution replaced immutable signature backing")
				}
				current := bgwriterReaderState(s)
				statsFunctionExec(t, s, "SAVEPOINT stats_view_denial")
				statsFunctionExec(t, admin, "REVOKE SELECT ON pg_catalog.pg_stat_subscription_stats FROM PUBLIC")
				result, err = subscriptionStatsNameExecute(t, e, s, ast, &d, &name)
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("denied held view published rows")
				}
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for view pg_stat_subscription_stats"})
				statsFunctionExec(t, s, "ROLLBACK TO stats_view_denial")
				if bgwriterReaderState(s) != current {
					t.Fatal("current view denial changed retained positive cut")
				}
				for _, argument := range []*bool{&no, nil, &yes} {
					statsFunctionExec(t, s, "SAVEPOINT stats_conjunction_view_denial")
					denied, err := executeConjunction(argument)
					if denied.Rows != nil {
						denied.Rows.Close()
						t.Fatal("SELECT-denied conjunction published rows")
					}
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for view pg_stat_subscription_stats"})
					statsFunctionExec(t, s, "ROLLBACK TO stats_conjunction_view_denial")
					if bgwriterReaderState(s) != current {
						t.Fatal("SELECT denial did not precede rejected conjunction capture")
					}
				}
				statsFunctionExec(t, admin, "GRANT SELECT ON pg_catalog.pg_stat_subscription_stats TO PUBLIC")
				result, err = subscriptionStatsNameExecute(t, e, s, ast, &d, &name)
				if err != nil {
					t.Fatal("same held definition after view regrant", err)
				}
				if retained {
					subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: fact})
				} else {
					subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: e.Statistics.Read(key)})
				}
				if bgwriterReaderState(s) != current {
					t.Fatal("view regrant replaced retained backing")
				}
			})
		}
	}
}
