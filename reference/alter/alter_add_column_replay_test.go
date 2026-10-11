package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnCapturedDefaultsReplay(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("CREATE SEQUENCE s START 1 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec("ALTER TABLE t ADD z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL), ADD v BIGINT DEFAULT nextval('s')", 0))
		want := fixture.Select("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z FROM t ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 12:00:00"}})
		run(want)
		bag := fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})
		bag.Bag = true
		run(bag)
		if !backend.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "nextval") {
				t.Fatal("lost original expressions", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
			run(bag)
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("captured-default PostgreSQL replay", err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, bag)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t; DROP SEQUENCE s"); err != nil {
					t.Fatal(err)
				}
			}
		}
		// Dump and Restore must not add a hidden sequence draw. Old stable values
		// remain literal data; future defaults retain current-session sensitivity.
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1)))
		run(fixture.Select("SELECT (z AT TIME ZONE 'UTC')::TEXT AS z,v FROM t WHERE id=3", []string{"z", "v"}, []string{"TEXT", "INT8"}, [][]any{{"2004-07-01 16:00:00", int64(4)}}))
		run(fixture.Select("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z FROM t WHERE id<3 ORDER BY id", want.Columns, want.Types, want.Rows))
	})
}
