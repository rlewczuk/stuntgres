package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterAddColumnCapturedDefaultsReplay(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		run(referenceExec("CREATE SEQUENCE s START 1 CACHE 1", 0))
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceExec("ALTER TABLE t ADD z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL), ADD v BIGINT DEFAULT nextval('s')", 0))
		want := referenceSelect("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z FROM t ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 12:00:00"}})
		run(want)
		bag := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})
		bag.bag = true
		run(bag)
		if !backend.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "nextval") {
				t.Fatal("lost original expressions", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
			run(bag)
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("captured-default PostgreSQL replay", err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, bag)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t; DROP SEQUENCE s"); err != nil {
					t.Fatal(err)
				}
			}
		}
		// Dump and Restore must not add a hidden sequence draw. Old stable values
		// remain literal data; future defaults retain current-session sensitivity.
		run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		run(referencePrepared(referenceExec("INSERT INTO t(id) VALUES(3)", 1)))
		run(referenceSelect("SELECT (z AT TIME ZONE 'UTC')::TEXT AS z,v FROM t WHERE id=3", []string{"z", "v"}, []string{"TEXT", "INT8"}, [][]any{{"2004-07-01 16:00:00", int64(4)}}))
		run(referenceSelect("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z FROM t WHERE id<3 ORDER BY id", want.columns, want.types, want.rows))
	})
}
