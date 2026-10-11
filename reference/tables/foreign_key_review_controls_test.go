package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignKeyRestoreSkippedCreate(t *testing.T) {
	for _, target := range []string{"skip_parent", "skip_absent_parent", "skip_absent_schema.skip_absent_parent"} {
		for _, populated := range []bool{false, true} {
			t.Run(target+"/"+map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
				foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					statements := []string{
						"CREATE TABLE skip_parent(id INT PRIMARY KEY)",
						"INSERT INTO skip_parent VALUES(1)",
						"CREATE TABLE skip_child(pid INT,note TEXT)",
					}
					if populated {
						statements = append(statements, "INSERT INTO skip_child VALUES(9,'original')")
					}
					skipped := "CREATE TABLE IF NOT EXISTS skip_child(pid INT CONSTRAINT unexpected_fk REFERENCES " + target + ")"
					statements = append(statements, skipped)
					if b.PG {
						for _, q := range statements {
							count := int64(0)
							if strings.HasPrefix(q, "INSERT") {
								count = 1
							}
							run(fixture.Exec(q, count))
						}
					} else {
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(strings.Join(statements, ";")+";")); err != nil {
							t.Fatal("skipped CREATE declaration was applied", err)
						}
					}
					var originalOID int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='skip_child'").Scan(&originalOID); err != nil || originalOID <= 0 {
						t.Fatal(originalOID, err)
					}
					var expected [][]any
					if populated {
						expected = [][]any{{int64(9), "original"}}
					}
					run(fixture.Select("SELECT pid,note FROM skip_child", []string{"pid", "note"}, []string{"INT4", "TEXT"}, expected))
					run(fixture.Select("SELECT f.conname FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='skip_child'", []string{"conname"}, []string{"NAME"}, nil))
					// Ordinary direct/prepared no-op semantics are independently observed too.
					var notices []fixture.Diagnostic
					_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					for _, prepared := range []bool{false, true} {
						notices = nil
						s := fixture.Exec(skipped, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "skip_child" already exists, skipping`}}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal("skipped FK CREATE notices", notices, want)
						}
						var current int64
						if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='skip_child'").Scan(&current); err != nil || current != originalOID {
							t.Fatal("skipped CREATE replaced relation", originalOID, current, err)
						}
					}
					run(fixture.Prepared(fixture.Exec("INSERT INTO skip_child VALUES(9,'after')", 1)))
					expected = [][]any{{int64(9), "after"}}
					if populated {
						expected = append(expected, []any{int64(9), "original"})
					}
					run(fixture.Select("SELECT pid,note FROM skip_child ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, expected))
					run(fixture.Select("SELECT f.conname FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='skip_child'", []string{"conname"}, []string{"NAME"}, nil))
					if b.PG {
						return
					}
					// A real, non-skipped invalid declaration still rejects the whole import.
					var before, after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
					held, err := b.DB.QueryContext(p.Ctx, "SELECT pid FROM skip_child ORDER BY note")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					script := "CREATE TABLE real_parent(id INT PRIMARY KEY);CREATE TABLE real_child(pid INT CONSTRAINT real_fk REFERENCES real_parent);INSERT INTO real_child VALUES(9);"
					d := foreignKeyChild("real_child", "real_fk", `Key (pid)=(9) is not present in table "real_parent".`)
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
					heldValues := [][]any{{int64(9)}}
					if populated {
						heldValues = append(heldValues, []any{int64(9)})
					}
					fixture.MigrationSmoke{T: t}.NullRows(held, []string{"pid"}, []string{"INT4"}, []bool{true}, heldValues)
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("real invalid FK import changed original", err)
					}
					var current int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='skip_child'").Scan(&current); err != nil || current != originalOID {
						t.Fatal("failed import replaced skipped child", originalOID, current, err)
					}
					run(fixture.Select("SELECT pid,note FROM skip_child ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, expected))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='real_child' OR relname='real_parent'", []string{"relname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
}

func TestReferenceForeignKeyRestoreCapturedAuthority(t *testing.T) {
	const alpha, beta = "fk_capture_alpha", "fk_capture_beta"
	fixture.RoleReferenceCleanup(t, []string{alpha, beta})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, alpha, beta)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, role := range []string{alpha, beta} {
				run(fixture.Exec("CREATE ROLE "+role, 0))
			}
			prefix := []string{"CREATE SCHEMA " + alpha + " AUTHORIZATION " + alpha, "CREATE SCHEMA " + beta + " AUTHORIZATION " + beta}
			alphaStatements := []string{
				"SET SESSION AUTHORIZATION " + alpha,
				"CREATE TABLE c(pid INT CONSTRAINT captured_fk REFERENCES p)",
				"INSERT INTO c VALUES(1)",
				"CREATE TABLE p(id INT PRIMARY KEY)",
				"INSERT INTO p VALUES(1)",
			}
			betaStatements := []string{"SET SESSION AUTHORIZATION " + beta, "CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(9)"}
			if b.PG {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				exec := func(q string) {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						count = 1
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, count))
				}
				for _, q := range prefix {
					exec(q)
				}
				// PostgreSQL ordinary CREATE requires the target first; native replay
				// below deliberately declares the same FK before its target exists.
				for _, i := range []int{0, 3, 4, 1, 2} {
					exec(alphaStatements[i])
				}
				for _, q := range betaStatements {
					exec(q)
				}
				exec("RESET SESSION AUTHORIZATION")
			} else {
				statements := append(append(prefix, alphaStatements...), betaStatements...)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(strings.Join(statements, ";")+";")); err != nil {
					t.Fatal("captured alpha declaration adopted final beta authority/path", err)
				}
				foreignKeyRoundtrip(t, p)
			}
			run(foreignKeyIntRows("SELECT pid FROM "+alpha+".c", "pid", 1))
			run(foreignKeyIntRows("SELECT id FROM "+alpha+".p", "id", 1))
			run(foreignKeyIntRows("SELECT id FROM "+beta+".p", "id", 9))
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='captured_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (pid) REFERENCES " + alpha + ".p(id)"}}))
			missing := foreignKeyChild("c", "captured_fk", `Key (pid)=(9) is not present in table "p".`)
			missing.Schema = alpha
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail("INSERT INTO "+alpha+".c VALUES(9)", missing, prepared))
			}
			run(fixture.Exec("CREATE TABLE protected_parent(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("INSERT INTO protected_parent VALUES(1)", 1))
			run(fixture.Exec("CREATE TABLE authority_sentinel(v INT)", 0))
			run(fixture.Exec("INSERT INTO authority_sentinel VALUES(91)", 1))
			denied := foreignKeyError("42501", "permission denied for table protected_parent", "", "", "")
			if b.PG {
				// The PostgreSQL fixture recreates public without PUBLIC USAGE.
				// Admit namespace lookup without granting table REFERENCES.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+alpha, 0))
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+alpha, 0))
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, foreignKeyFail("CREATE TABLE denied_child(pid INT CONSTRAINT denied_fk REFERENCES public.protected_parent)", denied, prepared))
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			} else {
				var before, after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				identityQueries := []string{
					"SELECT oid::bigint FROM pg_class WHERE relname='authority_sentinel'",
					"SELECT oid::bigint FROM pg_constraint WHERE conname='captured_fk'",
					"SELECT '" + alpha + ".c'::regclass::oid::bigint",
					"SELECT '" + alpha + ".p'::regclass::oid::bigint",
					"SELECT '" + beta + ".p'::regclass::oid::bigint",
					"SELECT '" + alpha + ".p_pkey'::regclass::oid::bigint",
					"SELECT '" + beta + ".p_pkey'::regclass::oid::bigint",
				}
				identities := make([]int64, len(identityQueries))
				for i, q := range identityQueries {
					if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&identities[i]); err != nil || identities[i] <= 0 {
						t.Fatal(q, identities[i], err)
					}
				}
				held, err := b.DB.QueryContext(p.Ctx, "SELECT v FROM authority_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				script := "CREATE SCHEMA " + alpha + " AUTHORIZATION " + alpha + ";CREATE TABLE protected_parent(id INT PRIMARY KEY);INSERT INTO protected_parent VALUES(1);SET SESSION AUTHORIZATION " + alpha + ";CREATE TABLE denied_child(pid INT CONSTRAINT denied_fk REFERENCES public.protected_parent);INSERT INTO denied_child VALUES(1);SET SESSION AUTHORIZATION postgres;"
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &denied)
				fixture.TransactionValues(t, held, 91)
				for i, q := range identityQueries {
					var current int64
					if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&current); err != nil || current != identities[i] {
						t.Fatal("failed authority import changed identity", q, identities[i], current, err)
					}
				}
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed captured-authority import changed target", err)
				}
			}
			run(foreignKeyIntRows("SELECT v FROM authority_sentinel", "v", 91))
			run(foreignKeyIntRows("SELECT pid FROM "+alpha+".c", "pid", 1))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='denied_child'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='denied_fk'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+alpha+".c VALUES(1)", 1)))
			run(foreignKeyFail("INSERT INTO "+alpha+".c VALUES(9)", missing, true))
			run(foreignKeyIntRows("SELECT pid FROM "+alpha+".c", "pid", 1, 1))
		})
	}
}
