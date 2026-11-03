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

func TestPGStatArchiverRoutineMetadata(t *testing.T) {
	e, other := engine.New(), engine.New()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3195}
	view := e.CatalogRead()
	p, ok := view.Procedure(address)
	q, exists := other.CatalogRead().Procedure(address)
	if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(3195) {
		t.Fatal("engine-owned descriptors and canonical immutable declaration", p, q)
	}
	if p.Builtin.Identity != (catalog.RoutineIdentity{ID: 3195, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_archiver"}) || p.RoutineKind != catalog.FunctionRoutine || p.Result != 2249 || p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.ReturnsSet() || p.RowEstimate() != 0 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 {
		t.Fatal("released noncallable archiver declaration", p, p.Builtin)
	}
	if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_archiver"}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil {
		t.Fatal("default PUBLIC authority and internal metadata", p)
	}
	outputs := p.OutputDeclaration()
	if outputs == nil || outputs != q.OutputDeclaration() || outputs.Len() != 7 || outputs.Inputs() != 0 {
		t.Fatal("owned immutable OUT declaration", outputs)
	}
	for i, name := range [...]string{"archived_count", "last_archived_wal", "last_archived_time", "failed_count", "last_failed_wal", "last_failed_time", "stats_reset"} {
		if outputs.Name(i) != name || outputs.Type(i) != ([...]ids.OID{20, 25, 1184, 20, 25, 1184, 1184})[i] {
			t.Fatal("released OUT field", i, outputs.Name(i), outputs.Type(i))
		}
	}
	if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
		t.Fatal("catalog descriptor authority", err)
	}
	for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
		if _, err := routine.LookupBuiltinRoutineDeclaration(provider, p.Name(), nil); err == nil {
			t.Fatal("view backing acquired public getter capability", provider)
		}
	}
}

func TestPGStatArchiverReaderAuthority(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, captured := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/retained"}[captured], func(t *testing.T) {
				e := engine.New()
				admin := enginetest.StartSession(t, e, nil)
				reader := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, admin, "CREATE ROLE archiver_authority_reader")
				statsFunctionExec(t, reader, "SET ROLE archiver_authority_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, reader, "ROLLBACK")
				var writer statistics.Writer
				writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [6]int64{3, 2}})
				writer.Flush(&e.Statistics, time.Unix(100, 0), true)
				owned := reflect.ValueOf(core.PayloadOf(reader)).Elem().FieldByName("statsReader")
				capture := func(denied bool) {
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
					d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatArchiverOID})
					ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
					e.Mu.Lock()
					source, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, d)
					if denied {
						if source != nil {
							source.Close()
							t.Fatal("denied current backing authority published a source")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_archiver"})
						return
					}
					if err != nil || source == nil {
						t.Fatal("admitted source capture", err)
					}
					if len(source.Anonymous) != 1 || len(source.Anonymous[0]) != 7 {
						source.Close()
						t.Fatal("missing detached archiver singleton")
					}
					if n, err := source.Anonymous[0][0].Integer(); err != nil || n != 3 {
						source.Close()
						t.Fatal("admitted source lost original retained positive cut", n, err)
					}
					source.Close()
				}
				if captured {
					capture(false)
					if owned.FieldByName("full").Bool() != (mode == "snapshot") || owned.FieldByName("shared").Index(int(statistics.Archiver)).FieldByName("Counters").Index(0).Int() != 3 {
						t.Fatal("actual private Reader cut absent")
					}
					writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [6]int64{5, 5}})
					writer.Flush(&e.Statistics, time.Unix(200, 0), true)
					if e.Statistics.ReadShared(statistics.Archiver).Counters[0] != 8 {
						t.Fatal("current provider did not diverge from retained cut")
					}
				}
				stamp := owned.FieldByName("stamp")
				wall, ext, loc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
				kinds, full := owned.FieldByName("kinds").Uint(), owned.FieldByName("full").Bool()
				// Exercise the native current authorization frontier directly. This
				// is not a PostgreSQL catalog-invalidation timing expectation.
				statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() FROM PUBLIC")
				capture(true)
				if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
					t.Fatal("denied admission initialized or cleared private Reader ownership")
				}
				if captured && owned.FieldByName("shared").Index(int(statistics.Archiver)).FieldByName("Counters").Index(0).Int() != 3 {
					t.Fatal("denied admission mutated retained positive family")
				}
				statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_archiver() TO PUBLIC")
				capture(false)
			})
		}
	}
}
