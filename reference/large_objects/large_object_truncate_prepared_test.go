package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncatePrepared(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928306, 928341)
		s.run(fixture.Select("SELECT oid::bigint,proname::text,proargtypes::text,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,prosrc FROM pg_proc WHERE oid IN (1004,3172) ORDER BY oid",
			[]string{"oid", "proname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc"},
			[]string{"INT8", "TEXT", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{
				{int64(1004), "lo_truncate", "23 23", int64(23), true, "v", "u", "be_lo_truncate"},
				{int64(3172), "lo_truncate64", "23 20", int64(23), true, "v", "u", "be_lo_truncate64"},
			}))
		s.create(928306, "616263646566")
		var retained fixture.Observed
		var retainedTypes []*sql.ColumnType
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			step := truncateCall(name, int64(0), 2)
			held, stmt := s.held(step.Query)
			func() {
				defer stmt.Close()
				// Prepare/Describe did not run the volatile call or allocate a handle.
				s.bytes(928306, 0, 20, "616263646566")
				s.pages(928306, []any{int64(0), int64(6), "616263646566"})
				s.run(fixture.Exec("BEGIN", 0))
				fd := s.open(928306, truncateWrite, 0)
				s.execute(held, truncateCall(name, fd, 2))
				s.bytes(928306, 0, 20, "6162")
				capture := truncateCall(name, fd, 7)
				capture.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					var err error
					retainedTypes, err = rows.ColumnTypes()
					if err != nil { t.Fatal(err) }
				}
				retained = s.execute(held, capture)
				s.bytes(928306, 0, 20, "61620000000000")
				s.run(fixture.Exec("COMMIT", 0))
				failure := truncateError(step.Query, "42704", "invalid large-object descriptor: 0", fd, 1)
				s.execute(held, failure)
				s.run(fixture.Exec("BEGIN", 0))
				fd = s.open(928306, truncateWrite, 0)
				s.execute(held, truncateCall(name, fd, 1))
				s.bytes(928306, 0, 20, "61")
				s.run(fixture.Exec("ROLLBACK", 0))
			}()
			// Restore the next independent name's input without truncating it.
			s.run(fixture.Exec("BEGIN", 0))
			fd := s.open(928306, truncateWrite, 0)
			s.run(truncateCall(name, fd, 6))
			s.run(truncateValue("lo_put(928306::oid,0::bigint,decode('616263646566','hex'))", "VOID", ""))
			s.run(fixture.Exec("COMMIT", 0))
		}
		assertRetained := func() {
			if len(retained.Columns) != 1 || retained.Columns[0] != "result" || len(retained.Rows) != 1 || len(retained.Rows[0]) != 1 || retained.Rows[0][0] != int64(0) || len(retainedTypes) != 1 || retainedTypes[0].DatabaseTypeName() != "INT4" {
				s.t.Fatalf("detached result changed: %+v types=%v", retained, retainedTypes)
			}
		}
		assertRetained()
		if !s.b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil { s.t.Fatal(err) }
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil { s.t.Fatal(err) }
			assertRetained()
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil { s.t.Fatal(err) }
			assertRetained()
		}

		// Independent copy of the parent consumer's exact nested SQL shape.
		// Fresh Prepare is not substituted for this actual held definition.
		peer := s.peer()
		defer peer.close()
		for _, heldRoute := range []bool{false, true} {
			s.create(928341, "6162")
			s.run(truncateValue("lo_put(928341::oid,2048::bigint,decode('63','hex'))", "VOID", ""))
			s.pages(928341, []any{int64(0), int64(2), "6162"}, []any{int64(1), int64(1), "63"})
			const q = "SELECT lo_truncate(lo_open(928341::oid,131072),1) AS result"
			step := fixture.Select(q, []string{"result"}, []string{"INT4"}, [][]any{{int64(0)}})
			if heldRoute {
				held, stmt := s.held(q)
				func() {
					defer stmt.Close()
					s.pages(928341, []any{int64(0), int64(2), "6162"}, []any{int64(1), int64(1), "63"})
					s.run(fixture.Exec("BEGIN", 0))
					s.execute(held, step)
				}()
			} else {
				s.run(fixture.Exec("BEGIN", 0))
				fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, step)
			}
			s.pages(928341, []any{int64(0), int64(1), "61"})
			peer.pages(928341, []any{int64(0), int64(2), "6162"}, []any{int64(1), int64(1), "63"})
			s.run(fixture.Exec("COMMIT", 0))
			peer.pages(928341, []any{int64(0), int64(1), "61"})
			s.run(truncateValue("lo_unlink(928341::oid)", "INT4", int64(1)))
		}
		assertRetained()
	})
}
