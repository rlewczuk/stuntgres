package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckReview2OrderLogicalRoundtrip(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			p := fixture.NewScenario(t)
			source, err := p.Memory.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, source, p.MemoryRecord, s) }
			run(fixture.Exec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
			detail := "constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"
			if mixed {
				run(fixture.Exec("CREATE VIEW zv AS SELECT nextval('s') AS n", 0))
				run(fixture.Exec("CREATE VIEW av AS SELECT n FROM zv", 0))
				detail = "view zv depends on sequence s\nview av depends on view zv\n" + detail
			}
			run(fixture.Exec("CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0) NO INHERIT)", 0))
			run(fixture.Exec("CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))", 0))
			if mixed {
				run(fixture.Exec("ALTER SEQUENCE s OWNED BY z.a", 0))
				run(fixture.Exec("CREATE VIEW zrows AS SELECT a FROM z", 0))
				run(fixture.Exec("CREATE VIEW arows AS SELECT a FROM zrows", 0))
			}
			run(fixture.Exec("INSERT INTO z VALUES(11)", 1))
			run(fixture.Exec("INSERT INTO a VALUES(22)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
				s.Prepared = prepared
				run(s)
			}
			dump := fixture.CheckDump(t, p)
			if !strings.Contains(string(dump), "NO INHERIT") || mixed && !strings.Contains(string(dump), `OWNED BY "public"."z"."a"`) {
				t.Fatal(string(dump))
			}
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			target := fixture.NewFixture(t, false, true)
			for range 2 {
				if err := stuntgres.Reset(target.Ctx, target.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump, fixture.CheckDump(t, target)) {
					t.Fatal("repeated logical replay changed stream")
				}
				fixture.CheckRun(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
				fixture.CheckRun(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
			}
			backends := target.Backends()
			if p.Postgres != nil {
				backends = append(backends, fixture.Backend{"postgres", p.Postgres, p.PostgresRecord, true})
			}
			for _, b := range backends {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					if b.PG {
						defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
						if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
							t.Fatal("unchanged PG owner stream", err)
						}
						for _, name := range []string{"z", "a", "s"} {
							var owner string
							if err := conn.QueryRowContext(p.Ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=$1::regclass", name).Scan(&owner); err != nil || owner != "postgres" {
								t.Fatal(name, owner, err)
							}
						}
					}
					run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Select("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(fixture.Select("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
					if mixed {
						run(fixture.Select("SELECT a FROM arows", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					if !b.PG && !bytes.Equal(dump, fixture.CheckDump(t, target)) {
						t.Fatal("nondeterministic logical redump")
					}
					run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
					if mixed {
						run(fixture.Select("SELECT n FROM av", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
					}
					run(fixture.Select("SELECT setval('s',-1)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(-1)}}))
					run(fixture.Prepared(fixture.ImplicitError("INSERT INTO z VALUES($1)", fixture.CheckViolation("z", "cz", "33"), 33)))
					run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					if mixed {
						run(fixture.Exec("DROP TABLE z CASCADE", 0))
						for _, q := range []string{"SELECT nextval('s')", "SELECT n FROM zv", "SELECT n FROM av", "SELECT a FROM zrows", "SELECT a FROM arows"} {
							rows, err := conn.QueryContext(p.Ctx, q)
							if rows != nil {
								rows.Close()
								t.Fatal("removed dependent exposed rows", q)
							}
							fixture.State(t, err, "42P01")
						}
						run(fixture.Exec("INSERT INTO a VALUES(33)", 1))
						run(fixture.Select("SELECT a FROM a ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}, {int64(33)}}))
					}
				})
			}
		})
	}
}
