package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
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
)

func TestPGStatBGWriterRoutineMetadata(t *testing.T) {
	e, other := engine.New(), engine.New()
	for i, id := range [...]ids.OID{2772, 2773, 2859, 3075} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
		view := e.CatalogRead()
		p, ok := view.Procedure(address)
		q, exists := other.CatalogRead().Procedure(address)
		if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(id) {
			t.Fatal("engine-owned descriptor and immutable declaration", p, q)
		}
		name := ([...]string{"pg_stat_get_bgwriter_buf_written_clean", "pg_stat_get_bgwriter_maxwritten_clean", "pg_stat_get_buf_alloc", "pg_stat_get_bgwriter_stat_reset_time"})[i]
		result := ids.OID(20)
		if i == 3 {
			result = 1184
		}
		if p.Builtin.Identity != (catalog.RoutineIdentity{ID: id, Namespace: ids.PGCatalogNamespaceOID, Name: name}) || p.Result != result || p.RoutineKind != catalog.FunctionRoutine || !p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.ReturnsSet() || p.RowEstimate() != 0 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 || p.OutputDeclaration() != nil {
			t.Fatal("released noncallable scalar declaration", p, p.Builtin)
		}
		if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: name}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil {
			t.Fatal("implicit PUBLIC authority and internal metadata", p)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
			t.Fatal(err)
		}
		for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
			if _, err := routine.LookupBuiltinRoutineDeclaration(provider, name, nil); err == nil {
				t.Fatal("view backing acquired callable capability", provider, name)
			}
		}
	}
}

func TestPGStatBGWriterReaderAuthority(t *testing.T) {
	t.Run("capture", testBGWriterCapture)
	t.Run("relation authority", testBGWriterRelationAuthority)
	for _, mode := range []string{"cache", "snapshot"} {
		for _, captured := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/retained"}[captured], func(t *testing.T) {
				e := engine.New()
				admin := enginetest.StartSession(t, e, nil)
				reader := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, admin, "CREATE ROLE bgwriter_authority_reader")
				statsFunctionExec(t, reader, "SET ROLE bgwriter_authority_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, reader, "ROLLBACK")
				var writer statistics.Writer
				writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [6]int64{3, 2}})
				writer.Flush(&e.Statistics, time.Unix(100, 0), true)
				owned := reflect.ValueOf(core.PayloadOf(reader)).Elem().FieldByName("statsReader")
				capture := func(denied bool, demand []int) {
					t.Helper()
					op := enginetest.TransactionOperation(t, reader)
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
					d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatBGWriterOID})
					ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
					e.Mu.Lock()
					source, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, d, demand)
					if denied {
						if source != nil {
							source.Close()
							t.Fatal("denied current backing authority published a source")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_bgwriter_buf_written_clean"})
						return
					}
					if err != nil || source == nil {
						t.Fatal("admitted source capture", err)
					}
					if len(source.Anonymous) != 1 || len(source.Anonymous[0]) != 4 {
						source.Close()
						t.Fatal("missing detached bgwriter singleton")
					}
					want := int64(3)
					if demand != nil && len(demand) == 0 {
						want = 0
					}
					if n, err := source.Anonymous[0][0].Integer(); err != nil || n != want {
						source.Close()
						t.Fatal("admitted source lost original retained positive cut", n, err)
					}
					source.Close()
				}
				capture(false, []int{})
				if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || !owned.FieldByName("stamp").IsZero() {
					t.Fatal("presence-only capture initialized the Reader")
				}
				if captured {
					capture(false, nil)
					if owned.FieldByName("full").Bool() != (mode == "snapshot") || owned.FieldByName("shared").Index(int(statistics.BGWriter)).FieldByName("Counters").Index(0).Int() != 3 {
						t.Fatal("actual private Reader cut absent")
					}
					writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [6]int64{5, 5}})
					writer.Flush(&e.Statistics, time.Unix(200, 0), true)
					if e.Statistics.ReadShared(statistics.BGWriter).Counters[0] != 8 {
						t.Fatal("current provider did not diverge from retained cut")
					}
				}
				stamp := owned.FieldByName("stamp")
				wall, ext, loc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
				kinds, full := owned.FieldByName("kinds").Uint(), owned.FieldByName("full").Bool()
				// Exercise the native current authorization frontier directly. This
				// is not a PostgreSQL catalog-invalidation timing expectation.
				statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC")
				capture(true, nil)
				capture(false, []int{})
				if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
					t.Fatal("denied admission initialized or cleared private Reader ownership")
				}
				if captured && owned.FieldByName("shared").Index(int(statistics.BGWriter)).FieldByName("Counters").Index(0).Int() != 3 {
					t.Fatal("denied admission mutated retained positive family")
				}
				capture(false, []int{3})
				statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC")
				capture(false, nil)
			})
		}
	}
}