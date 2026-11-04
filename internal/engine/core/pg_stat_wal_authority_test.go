package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
)

func testWALReaderAuthority(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/retained"}[retained], func(t *testing.T) {
				e := engine.New()
				admin := enginetest.StartSession(t, e, nil)
				reader := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, admin, "CREATE ROLE wal_authority_reader")
				resetStatsExec(t, admin, "SELECT pg_stat_reset_shared('wal')")
				before := e.Statistics.ReadShared(statistics.SharedWAL)
				if before.ResetAt.IsZero() {
					t.Fatal("positive admitted reset required")
				}
				statsFunctionExec(t, reader, "SET ROLE wal_authority_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, reader, "ROLLBACK")
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
					d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatWALOID})
					ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
					e.Mu.Lock()
					source, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, d, nil)
					if denied {
						if source != nil {
							source.Close()
							t.Fatal("denied current backing authority published source")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_wal"})
						return
					}
					if err != nil || source == nil {
						t.Fatal("admitted WAL capture", err)
					}
					defer source.Close()
					if len(source.Anonymous) != 1 || len(source.Anonymous[0]) != 5 {
						t.Fatal("missing detached singleton")
					}
					if at, err := source.Anonymous[0][4].TimestampTime(); err != nil || !at.Equal(before.ResetAt) {
						t.Fatal("lost original positive reset cut", at, before.ResetAt, err)
					}
				}
				if retained {
					capture(false)
					resetStatsExec(t, admin, "SELECT pg_stat_reset_shared('wal')")
					if !e.Statistics.ReadShared(statistics.SharedWAL).ResetAt.After(before.ResetAt) {
						t.Fatal("provider must distinguish retained cut")
					}
				}
				stamp := owned.FieldByName("stamp")
				wall, ext, loc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
				kinds, full := owned.FieldByName("kinds").Uint(), owned.FieldByName("full").Bool()
				statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() FROM PUBLIC")
				capture(true)
				if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
					t.Fatal("denial initialized or cleared private Reader")
				}
				if retained && !owned.FieldByName("shared").Index(int(statistics.SharedWAL)).FieldByName("ResetAt").Equal(reflect.ValueOf(before.ResetAt)) {
					t.Fatal("denial mutated retained reset authority")
				}
				statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() TO PUBLIC")
				capture(false)
			})
		}
	}
}
