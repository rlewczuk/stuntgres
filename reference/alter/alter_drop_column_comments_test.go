package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDropColumnComments(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			step := func(q string, n int64) { s := fixture.Exec(q, n); s.Prepared = prepared; p.Step(t, s) }
			step("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0)
			step("INSERT INTO t VALUES(1,'removed',9)", 1)
			for _, q := range []string{"COMMENT ON TABLE t IS 'table keep'", "COMMENT ON COLUMN t.b IS 'removed comment'", "COMMENT ON COLUMN t.c IS 'survivor comment'"} {
				step(q, 0)
			}
			dump := func() string {
				t.Helper()
				var b bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &b); err != nil {
					t.Fatal(err)
				}
				return b.String()
			}
			oracle := func(b, c string) {
				t.Helper()
				if p.Postgres == nil {
					return
				}
				for _, probe := range []struct{ q, want string }{{"SELECT obj_description('t'::regclass,'pg_class')", "table keep"}, {"SELECT col_description('t'::regclass,2)", b}, {"SELECT col_description('t'::regclass,3)", c}} {
					var got sql.NullString
					if err := p.Postgres.QueryRowContext(p.Ctx, probe.q).Scan(&got); err != nil || got.Valid != (probe.want != "") || got.String != probe.want {
						t.Fatal(probe.q, got, probe.want, err)
					}
				}
			}
			before := dump()
			p.Step(t, fixture.RoleReferenceError("ALTER TABLE t DROP missing", "42703", `column "missing" of relation "t" does not exist`, "", prepared, "execute"))
			if dump() != before {
				t.Fatal("native failing drop changed comments")
			}
			oracle("removed comment", "survivor comment")
			step("ALTER TABLE t DROP IF EXISTS missing", 0)
			if dump() != before {
				t.Fatal("missing skip changed native metadata")
			}
			oracle("removed comment", "survivor comment")
			step("ALTER TABLE t DROP b", 0)
			oracle("", "survivor comment")
			if p.Postgres != nil {
				var droppedAndLive bool
				if err := p.Postgres.QueryRowContext(p.Ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='t'::regclass AND attnum=2 AND attisdropped) AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c' AND attnum=3 AND NOT attisdropped)`).Scan(&droppedAndLive); err != nil || !droppedAndLive {
					t.Fatal("oracle stable attribute lifecycle", droppedAndLive, err)
				}
			}
			canonical := dump()
			if strings.Contains(canonical, "removed comment") || strings.Contains(canonical, `COMMENT ON COLUMN "public"."t"."b"`) || !strings.Contains(canonical, `COMMENT ON COLUMN "public"."t"."c" IS E'survivor comment';`) || !strings.Contains(canonical, `COMMENT ON TABLE "public"."t" IS E'table keep';`) {
				t.Fatal(canonical)
			}
			step("COMMENT ON COLUMN t.c IS 'updated survivor'", 0)
			oracle("", "updated survivor")
			canonical = dump()
			if !strings.Contains(canonical, `COMMENT ON COLUMN "public"."t"."c" IS E'updated survivor';`) {
				t.Fatal(canonical)
			}
			p.Step(t, fixture.Select("SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}}))
			for i := 0; i < 2; i++ {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(canonical)); err != nil {
					t.Fatal(err)
				}
				if dump() != canonical {
					t.Fatal("native canonical comment roundtrip changed", dump())
				}
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}}))
				oracle("", "updated survivor")
			}
			if p.Postgres == nil {
				return
			}
			replay, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer replay.Close()
				defer replay.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err = replay.ExecContext(p.Ctx, "DROP TABLE t"); err != nil {
					t.Fatal(err)
				}
				if _, err = replay.ExecContext(p.Ctx, canonical); err != nil {
					t.Fatal("pinned PostgreSQL rejected unchanged native comment dump", err)
				}
			}()
			var sub int
			var comment string
			if err = p.Postgres.QueryRowContext(p.Ctx, `SELECT attnum,col_description(attrelid,attnum) FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c' AND NOT attisdropped`).Scan(&sub, &comment); err != nil || sub != 2 || comment != "updated survivor" {
				t.Fatal("replayed named survivor metadata", sub, comment, err)
			}
			if err = p.Postgres.QueryRowContext(p.Ctx, `SELECT obj_description('t'::regclass,'pg_class')`).Scan(&comment); err != nil || comment != "table keep" {
				t.Fatal("replayed table metadata", comment, err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}}))
		})
	}
}
