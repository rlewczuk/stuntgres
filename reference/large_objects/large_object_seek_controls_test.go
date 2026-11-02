package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectSeekControls(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928408)
		s.create(928408, "616263")
		s.run(truncateValue("lo_put(928408::oid,4294967296::bigint,decode('666f6f','hex'))", "VOID", ""))
		verify := func() {
			s.pages(928408, []any{int64(0), int64(3), "616263"}, []any{int64(2097152), int64(3), "666f6f"})
			s.bytes(928408, 0, 5, "6162630000")
			s.bytes(928408, 4294967294, 5, "0000666f6f")
		}
		verify()
		type retainedCall struct { held fixture.HeldStatement; stmt *sql.Stmt; step fixture.Step }
		var calls []retainedCall
		for _, api := range seekAPIs {
			for _, step := range []fixture.Step{
				seekValue(api, int64(0), 2, 0, int64(2)),
				tellValue(api, int64(0), int64(2)),
				truncateValue("pg_catalog."+api.seek+"(0::integer,2::"+api.offsetType+",0::integer)", api.resultType, int64(2)),
				truncateValue("pg_catalog."+api.tell+"(0::integer)", api.resultType, int64(2)),
			} {
				held, stmt := s.held(step.Query)
				defer stmt.Close()
				calls = append(calls, retainedCall{held, stmt, step})
			}
		}
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928408, truncateWrite, 0)
		s.run(tell64(fd, 0))
		var detached fixture.Observed
		var detachedTypes []*sql.ColumnType
		for i, call := range calls {
			step := call.step
			if i == 0 {
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					var err error
					detachedTypes, err = rows.ColumnTypes()
					if err != nil { t.Fatal(err) }
				}
				detached = s.execute(call.held, step)
			} else { s.execute(call.held, step) }
		}
		s.run(tell64(fd, 2))
		s.run(fixture.Exec("COMMIT", 0))
		for _, call := range calls { s.execute(call.held, seekFailure(call.step, "42704", "invalid large-object descriptor: 0")) }
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928408, truncateWrite, 0)
		s.run(tell64(fd, 0))
		for _, call := range calls { s.execute(call.held, call.step) }
		s.run(fixture.Exec("ROLLBACK", 0))
		assertDetached := func() {
			if len(detached.Columns) != 1 || detached.Columns[0] != "result" || len(detached.Rows) != 1 || len(detached.Rows[0]) != 1 || detached.Rows[0][0] != int64(2) || len(detachedTypes) != 1 || detachedTypes[0].DatabaseTypeName() != "INT4" {
				s.t.Fatalf("detached scalar/metadata changed: %+v types=%v", detached, detachedTypes)
			}
		}
		assertDetached()
		verify()
		if s.b.PG { return } // Only public native controls lack a PostgreSQL counterpart.

		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928408, truncateWrite, 0)
		s.run(seek64(fd, 10, 0, 10))
		s.run(truncateValue("lowrite($1::integer,decode('5a','hex'))", "INT4", int64(1), fd))
		s.run(tell64(fd, 11))
		var saved bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &saved); err != nil { s.t.Fatal(err) }
		for _, forbidden := range []string{"lo_lseek(", "lo_lseek64(", "lo_tell(", "lo_tell64(", "loread(", "lowrite(", "lo_close("} {
			if strings.Contains(saved.String(), forbidden) { s.t.Fatal("serialized descriptor/cursor operation", forbidden) }
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil { s.t.Fatal(err) }
		_, err := calls[0].stmt.ExecContext(s.p.Ctx, calls[0].step.Args...)
		fixture.TransactionOrdinary(s.t, err, "stuntgres: catalog replaced before publication")
		s.run(fixture.Exec("ROLLBACK", 0))
		for _, call := range calls { s.execute(call.held, seekFailure(call.step, "42704", "invalid large-object descriptor: 0")) }
		s.run(fixture.Select("SELECT oid::bigint FROM pg_largeobject_metadata WHERE oid=928408::oid ORDER BY oid", []string{"oid"}, []string{"INT8"}, nil))
		assertDetached()
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(saved.Bytes())); err != nil { s.t.Fatal(err) }
		verify() // Dump excluded the active transaction's private byte at ten.
		s.bytes(928408, 8, 5, "0000000000")
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928408, truncateWrite, 0)
		s.run(tell64(fd, 0))
		for _, call := range calls { s.execute(call.held, call.step) }
		s.run(seek64(fd, 4294967296, 0, 4294967296))
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(saved.Bytes())); err != nil { s.t.Fatal(err) }
		_, err = calls[1].stmt.ExecContext(s.p.Ctx, calls[1].step.Args...)
		fixture.TransactionOrdinary(s.t, err, "stuntgres: catalog replaced before publication")
		s.run(fixture.Exec("ROLLBACK", 0))
		for _, call := range calls { s.execute(call.held, seekFailure(call.step, "42704", "invalid large-object descriptor: 0")) }
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928408, truncateWrite, 0)
		s.run(tell64(fd, 0))
		for _, call := range calls { s.execute(call.held, call.step) }
		s.run(seek64(fd, 0, 2, 4294967299))
		s.run(fixture.Exec("ROLLBACK", 0))
		verify()
		assertDetached()
	})
}
