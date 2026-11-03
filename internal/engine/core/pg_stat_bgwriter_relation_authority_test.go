package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func testBGWriterRelationAuthority(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/retained"}[retained], func(t *testing.T) {
				e := engine.New()
				admin := enginetest.StartSession(t, e, nil)
				reader := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, admin, "CREATE ROLE bgwriter_column_reader")
				statsFunctionExec(t, reader, "SET ROLE bgwriter_column_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, reader, "ROLLBACK")
				var writer statistics.Writer
				writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [8]int64{3, 5, 7}})
				writer.Flush(&e.Statistics, time.Unix(100, 0), true)
				fact := e.Statistics.ReadShared(statistics.BGWriter)
				ast, description := enginetest.PreparedDescription(t, e, reader, "SELECT * FROM pg_stat_bgwriter")
				read := func() {
					t.Helper()
					result, err := enginetest.PreparedExecute(e, reader, ast, &description)
					if err != nil {
						t.Fatal("admitted typed bgwriter read", err)
					}
					bgwriterOwnerRow(t, result, fact)
				}
				if retained {
					read()
				}
				owned := reflect.ValueOf(core.PayloadOf(reader)).Elem().FieldByName("statsReader")
				full, kinds := owned.FieldByName("full").Bool(), owned.FieldByName("kinds").Uint()
				stamp := owned.FieldByName("stamp")
				wall, ext, loc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
				denied := func() {
					t.Helper()
					statsFunctionExec(t, reader, "SAVEPOINT bgwriter_denied")
					result, err := enginetest.PreparedExecute(e, reader, ast, &description)
					if result.Rows != nil {
						result.Rows.Close()
						t.Fatal("denied view published rows")
					}
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for view pg_stat_bgwriter"})
					if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
						t.Fatal("denied relation/column changed Reader ownership")
					}
					if retained && owned.FieldByName("shared").Index(int(statistics.BGWriter)).FieldByName("Counters").Index(0).Int() != 3 {
						t.Fatal("denial replaced retained positive family")
					}
					statsFunctionExec(t, reader, "ROLLBACK TO SAVEPOINT bgwriter_denied", "RELEASE SAVEPOINT bgwriter_denied")
				}
				statsFunctionExec(t, admin, "REVOKE SELECT ON pg_stat_bgwriter FROM PUBLIC")
				denied()
				statsFunctionExec(t, admin, "GRANT SELECT (stats_reset) ON pg_stat_bgwriter TO bgwriter_column_reader")
				denied()
				columnAST, columnDescription := enginetest.PreparedDescription(t, e, reader, "SELECT stats_reset FROM pg_stat_bgwriter")
				columnResult, err := enginetest.PreparedExecute(e, reader, columnAST, &columnDescription)
				if err != nil || columnResult.Rows == nil {
					t.Fatal("admitted reset projection", err)
				}
				defer columnResult.Rows.Close()
				columns := columnResult.Rows.Schema()
				if len(columns) != 1 || columns[0].Usage.Type.ID() != sqltype.Timestamptz {
					t.Fatal("reset projection timestamp schema", columns)
				}
				var cell [1]sqltype.Value
				if present, err := columnResult.Rows.Next(t.Context(), cell[:]); err != nil || !present {
					t.Fatal("reset projection singleton", present, err)
				}
				if at, err := cell[0].TimestampTime(); err != nil || !at.Equal(fact.ResetAt) {
					t.Fatal("reset projection instant", at, fact.ResetAt, err)
				}
				if present, err := columnResult.Rows.Next(t.Context(), cell[:]); err != nil || present {
					t.Fatal("reset projection extra row", present, err)
				}
				columnResult.Rows.Close()
				statsFunctionExec(t, admin, "GRANT SELECT ON pg_stat_bgwriter TO PUBLIC")
				read()
			})
		}
	}
}
