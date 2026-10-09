package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCheckReview2OrderLogicalRoundtrip(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			p := newReferenceScenario(t)
			source, err := p.memory.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, source, p.memoryRecord, s) }
			run(referenceExec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
			detail := "constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"
			if mixed {
				run(referenceExec("CREATE VIEW zv AS SELECT nextval('s') AS n", 0))
				run(referenceExec("CREATE VIEW av AS SELECT n FROM zv", 0))
				detail = "view zv depends on sequence s\nview av depends on view zv\n" + detail
			}
			run(referenceExec("CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0) NO INHERIT)", 0))
			run(referenceExec("CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))", 0))
			if mixed {
				run(referenceExec("ALTER SEQUENCE s OWNED BY z.a", 0))
				run(referenceExec("CREATE VIEW zrows AS SELECT a FROM z", 0))
				run(referenceExec("CREATE VIEW arows AS SELECT a FROM zrows", 0))
			}
			run(referenceExec("INSERT INTO z VALUES(11)", 1))
			run(referenceExec("INSERT INTO a VALUES(22)", 1))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
				s.prepared = prepared
				run(s)
			}
			dump := referenceCheckDump(t, p)
			if !strings.Contains(string(dump), "NO INHERIT") || mixed && !strings.Contains(string(dump), `OWNED BY "public"."z"."a"`) {
				t.Fatal(string(dump))
			}
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			target := newReferenceFixture(t, false, true)
			for range 2 {
				if err := stuntgres.Reset(target.ctx, target.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump, referenceCheckDump(t, target)) {
					t.Fatal("repeated logical replay changed stream")
				}
				referenceCheckRun(t, target.ctx, target.memory, target.memoryRecord, referenceSelect("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
				referenceCheckRun(t, target.ctx, target.memory, target.memoryRecord, referenceSelect("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
			}
			backends := target.backends()
			if p.postgres != nil {
				backends = append(backends, referenceBackend{"postgres", p.postgres, p.postgresRecord, true})
			}
			for _, b := range backends {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					if b.pg {
						defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
						if _, err := conn.ExecContext(p.ctx, string(dump)); err != nil {
							t.Fatal("unchanged PG owner stream", err)
						}
						for _, name := range []string{"z", "a", "s"} {
							var owner string
							if err := conn.QueryRowContext(p.ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=$1::regclass", name).Scan(&owner); err != nil || owner != "postgres" {
								t.Fatal(name, owner, err)
							}
						}
					}
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
						s.prepared = prepared
						run(s)
					}
					run(referenceSelect("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(referenceSelect("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
					if mixed {
						run(referenceSelect("SELECT a FROM arows", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					if !b.pg && !bytes.Equal(dump, referenceCheckDump(t, target)) {
						t.Fatal("nondeterministic logical redump")
					}
					run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
					if mixed {
						run(referenceSelect("SELECT n FROM av", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
					}
					run(referenceSelect("SELECT setval('s',-1)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(-1)}}))
					run(referencePrepared(referenceImplicitError("INSERT INTO z VALUES($1)", referenceCheckViolation("z", "cz", "33"), 33)))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(referenceSelect("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					if mixed {
						run(referenceExec("DROP TABLE z CASCADE", 0))
						for _, q := range []string{"SELECT nextval('s')", "SELECT n FROM zv", "SELECT n FROM av", "SELECT a FROM zrows", "SELECT a FROM arows"} {
							rows, err := conn.QueryContext(p.ctx, q)
							if rows != nil {
								rows.Close()
								t.Fatal("removed dependent exposed rows", q)
							}
							referenceState(t, err, "42P01")
						}
						run(referenceExec("INSERT INTO a VALUES(33)", 1))
						run(referenceSelect("SELECT a FROM a ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}, {int64(33)}}))
					}
				})
			}
		})
	}
}
