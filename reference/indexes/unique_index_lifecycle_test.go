package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueIndexLifecycle(t *testing.T) {
	fixture.UniqueReferenceSchemaCleanup(t, "ui_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ui_t(extra INT,a INT,b TEXT,p TEXT)", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b) INCLUDE(p) NULLS NOT DISTINCT", 0))
			run(fixture.Exec("INSERT INTO ui_t VALUES(0,1,'x','payload'),(0,NULL,'n','null')", 2))
			var oid int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal(oid, err)
			}
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ui_ab'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("ALTER INDEX ui_ab RENAME TO ui_renamed", 0))
			run(fixture.Exec("ALTER TABLE ui_t RENAME b TO label", 0))
			run(fixture.Exec("ALTER TABLE ui_t RENAME TO ui_parent", 0))
			var current int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ui_renamed'").Scan(&current); err != nil || current != oid {
				t.Fatal("rename identity", oid, current, err)
			}
			run(fixture.Select("SELECT pg_get_indexdef('ui_renamed'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX ui_renamed ON public.ui_parent USING btree (a, label) INCLUDE (p) NULLS NOT DISTINCT"}}))
			run(fixture.ErrorDiagnostic("INSERT INTO ui_parent VALUES(9,1,'x','different')", fixture.UniqueReferenceDuplicate("ui_parent", "ui_renamed", "Key (a, label)=(1, x) already exists.")))
			run(fixture.Exec("ALTER TABLE ui_parent DROP extra RESTRICT", 0))
			run(fixture.Exec("CREATE SCHEMA ui_moved", 0))
			run(fixture.Exec("CREATE TABLE ui_moved.ui_renamed(v INT)", 0))
			run(fixture.ImplicitError("ALTER TABLE ui_parent SET SCHEMA ui_moved", fixture.Diagnostic{Code: "42P07", Message: `relation "ui_renamed" already exists in schema "ui_moved"`}))
			run(fixture.Exec("DROP TABLE ui_moved.ui_renamed", 0))
			run(fixture.Exec("ALTER TABLE ui_parent SET SCHEMA ui_moved", 0))
			want := fixture.UniqueReferenceDuplicate("ui_parent", "ui_renamed", "Key (a, label)=(1, x) already exists.")
			want.Schema = "ui_moved"
			run(fixture.ErrorDiagnostic("INSERT INTO ui_moved.ui_parent VALUES(1,'x','again')", want))
			q := "INSERT INTO ui_moved.ui_parent VALUES($1,$2,$3)"
			held, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("DROP INDEX ui_moved.ui_renamed", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 1, int64(1), "x", "again"))
			run(fixture.Exec("DELETE FROM ui_moved.ui_parent WHERE p='again'", 1))
			run(fixture.Exec("CREATE UNIQUE INDEX ui_renamed ON ui_moved.ui_parent(a,label) INCLUDE(p)", 0))
			run(fixture.Exec("ALTER TABLE ui_moved.ui_parent DROP p RESTRICT", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_renamed'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO ui_moved.ui_parent VALUES(1,'x')", 1))
			run(fixture.Exec("DROP SCHEMA ui_moved CASCADE", 0))
		})
	}
}

func TestReferenceUniqueIndexType(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(fmt.Sprintf("%s/prepared=%t", b.Name, prepared), func(t *testing.T) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x'),(2,'x')", 2))
				var oid int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				failure := fixture.ErrorDiagnostic("ALTER TABLE ui_t ALTER a TYPE BIGINT USING 0", uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", "Key (a, b)=(0, x) is duplicated."))
				failure.ErrorStage = "execute"
				run(failure)
				var retained int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&retained); err != nil || retained != oid {
					t.Fatal(oid, retained, err)
				}
				run(fixture.Select("SELECT a,b FROM ui_t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "x"}}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT before_type", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(3,'x')", 1))
				run(failure)
				run(fixture.Exec("ROLLBACK TO before_type", 0))
				run(fixture.Exec("DELETE FROM ui_t WHERE a=2", 1))
				run(fixture.Exec("ALTER TABLE ui_t ALTER a TYPE BIGINT USING 0", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES(0,'x')", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(0, x) already exists.")))
				run(fixture.Exec("ROLLBACK TO before_type", 0))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES(1,'x')", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(1, x) already exists.")))
				run(fixture.Select("SELECT a,b FROM ui_t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "x"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var actualDump string
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			if b.PG && actualDump != "" {
				if _, err := b.DB.ExecContext(p.Ctx, actualDump); err != nil {
					t.Fatal("actual native dump PostgreSQL replay", err)
				}
			} else {
				run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a DESC NULLS LAST,b) NULLS NOT DISTINCT", 0))
				run(fixture.Exec("CREATE TABLE ui_attached(k INT CONSTRAINT ui_attached_u UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x'),(NULL,'n')", 2))
			}
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				actualDump = dump.String()
				if strings.Count(actualDump, "CREATE UNIQUE INDEX") != 1 || strings.Count(actualDump, "ui_attached_u") != 1 {
					t.Fatal("attachment serialization", actualDump)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_t' OR relname='ui_ab'", []string{"relname"}, []string{"NAME"}, nil))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump)); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != actualDump {
					t.Fatal("noncanonical replay", err, again.String())
				}
			}
			run(fixture.Select("SELECT a,b FROM ui_t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, "n"}}))
			q := "INSERT INTO ui_t VALUES($1,$2)"
			held, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(null, n) already exists."), nil, "n"))
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, int64(2), "y"))
			if !b.PG {
				run(fixture.Exec("CREATE TABLE ui_sentinel(k INT)", 0))
				run(fixture.Exec("INSERT INTO ui_sentinel VALUES(7)", 1))
				old, err := b.DB.QueryContext(p.Ctx, "SELECT a,b FROM ui_t ORDER BY a NULLS LAST")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				bad := actualDump + "INSERT INTO public.ui_t VALUES(NULL,'n');\n"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				want := fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(null, n) already exists.")
				fixture.AssertDiagnostic(t, err, &want)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump+"CREATE UNIQUE INDEX broken ON ui_t(;")); err == nil {
					t.Fatal("malformed restore accepted")
				}
				run(fixture.Select("SELECT k FROM ui_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
				for _, want := range [][]any{{int64(1), "x"}, {int64(2), "y"}, {nil, "n"}} {
					if !old.Next() {
						t.Fatal("retained row lost", old.Err())
					}
					var a any
					var label string
					if err := old.Scan(&a, &label); err != nil || a != want[0] || label != want[1] {
						t.Fatal(a, label, err, want)
					}
				}
				if old.Next() || old.Err() != nil {
					t.Fatal(old.Err())
				}
				old.Close()
			}
		})
	}
}
