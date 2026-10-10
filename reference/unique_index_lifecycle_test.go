package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUniqueIndexLifecycle(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "ui_moved")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
			run(referenceExec("CREATE TABLE ui_t(extra INT,a INT,b TEXT,p TEXT)", 0))
			run(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b) INCLUDE(p) NULLS NOT DISTINCT", 0))
			run(referenceExec("INSERT INTO ui_t VALUES(0,1,'x','payload'),(0,NULL,'n','null')", 2))
			var oid int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal(oid, err)
			}
			run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='ui_ab'", []string{"conname"}, []string{"NAME"}, nil))
			run(referenceExec("ALTER INDEX ui_ab RENAME TO ui_renamed", 0))
			run(referenceExec("ALTER TABLE ui_t RENAME b TO label", 0))
			run(referenceExec("ALTER TABLE ui_t RENAME TO ui_parent", 0))
			var current int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ui_renamed'").Scan(&current); err != nil || current != oid {
				t.Fatal("rename identity", oid, current, err)
			}
			run(referenceSelect("SELECT pg_get_indexdef('ui_renamed'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX ui_renamed ON public.ui_parent USING btree (a, label) INCLUDE (p) NULLS NOT DISTINCT"}}))
			run(referenceErrorDiagnostic("INSERT INTO ui_parent VALUES(9,1,'x','different')", uniqueReferenceDuplicate("ui_parent", "ui_renamed", "Key (a, label)=(1, x) already exists.")))
			run(referenceExec("ALTER TABLE ui_parent DROP extra RESTRICT", 0))
			run(referenceExec("CREATE SCHEMA ui_moved", 0))
			run(referenceExec("CREATE TABLE ui_moved.ui_renamed(v INT)", 0))
			run(referenceImplicitError("ALTER TABLE ui_parent SET SCHEMA ui_moved", referenceDiagnostic{Code: "42P07", Message: `relation "ui_renamed" already exists in schema "ui_moved"`}))
			run(referenceExec("DROP TABLE ui_moved.ui_renamed", 0))
			run(referenceExec("ALTER TABLE ui_parent SET SCHEMA ui_moved", 0))
			want := uniqueReferenceDuplicate("ui_parent", "ui_renamed", "Key (a, label)=(1, x) already exists.")
			want.Schema = "ui_moved"
			run(referenceErrorDiagnostic("INSERT INTO ui_moved.ui_parent VALUES(1,'x','again')", want))
			q := "INSERT INTO ui_moved.ui_parent VALUES($1,$2,$3)"
			held, err := a.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec("DROP INDEX ui_moved.ui_renamed", 0))
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: q}, b.recorder, referenceExec(q, 1, int64(1), "x", "again"))
			run(referenceExec("DELETE FROM ui_moved.ui_parent WHERE p='again'", 1))
			run(referenceExec("CREATE UNIQUE INDEX ui_renamed ON ui_moved.ui_parent(a,label) INCLUDE(p)", 0))
			run(referenceExec("ALTER TABLE ui_moved.ui_parent DROP p RESTRICT", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_renamed'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceExec("INSERT INTO ui_moved.ui_parent VALUES(1,'x')", 1))
			run(referenceExec("DROP SCHEMA ui_moved CASCADE", 0))
		})
	}
}

func TestReferenceUniqueIndexType(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(fmt.Sprintf("%s/prepared=%t", b.name, prepared), func(t *testing.T) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) {
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, a, b.recorder, s)
				}
				run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x'),(2,'x')", 2))
				var oid int64
				if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				failure := referenceErrorDiagnostic("ALTER TABLE ui_t ALTER a TYPE BIGINT USING 0", uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", "Key (a, b)=(0, x) is duplicated."))
				failure.errorStage = "execute"
				run(failure)
				var retained int64
				if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ui_ab'").Scan(&retained); err != nil || retained != oid {
					t.Fatal(oid, retained, err)
				}
				run(referenceSelect("SELECT a,b FROM ui_t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "x"}}))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SAVEPOINT before_type", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(3,'x')", 1))
				run(failure)
				run(referenceExec("ROLLBACK TO before_type", 0))
				run(referenceExec("DELETE FROM ui_t WHERE a=2", 1))
				run(referenceExec("ALTER TABLE ui_t ALTER a TYPE BIGINT USING 0", 0))
				run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES(0,'x')", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(0, x) already exists.")))
				run(referenceExec("ROLLBACK TO before_type", 0))
				run(referenceExec("COMMIT", 0))
				run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES(1,'x')", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(1, x) already exists.")))
				run(referenceSelect("SELECT a,b FROM ui_t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "x"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexNativeControls(t *testing.T) {
	p := newReferenceScenario(t)
	var actualDump string
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			if b.pg && actualDump != "" {
				if _, err := b.db.ExecContext(p.ctx, actualDump); err != nil {
					t.Fatal("actual native dump PostgreSQL replay", err)
				}
			} else {
				run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a DESC NULLS LAST,b) NULLS NOT DISTINCT", 0))
				run(referenceExec("CREATE TABLE ui_attached(k INT CONSTRAINT ui_attached_u UNIQUE)", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x'),(NULL,'n')", 2))
			}
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				actualDump = dump.String()
				if strings.Count(actualDump, "CREATE UNIQUE INDEX") != 1 || strings.Count(actualDump, "ui_attached_u") != 1 {
					t.Fatal("attachment serialization", actualDump)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_t' OR relname='ui_ab'", []string{"relname"}, []string{"NAME"}, nil))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(actualDump)); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != actualDump {
					t.Fatal("noncanonical replay", err, again.String())
				}
			}
			run(referenceSelect("SELECT a,b FROM ui_t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, "n"}}))
			q := "INSERT INTO ui_t VALUES($1,$2)"
			held, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := referenceHeldStatement{referenceExecutor: b.db, statement: held, query: q}
			referenceRunStep(t, p.ctx, executor, b.recorder, referenceErrorDiagnostic(q, uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(null, n) already exists."), nil, "n"))
			referenceRunStep(t, p.ctx, executor, b.recorder, referenceExec(q, 1, int64(2), "y"))
			if !b.pg {
				run(referenceExec("CREATE TABLE ui_sentinel(k INT)", 0))
				run(referenceExec("INSERT INTO ui_sentinel VALUES(7)", 1))
				old, err := b.db.QueryContext(p.ctx, "SELECT a,b FROM ui_t ORDER BY a NULLS LAST")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				bad := actualDump + "INSERT INTO public.ui_t VALUES(NULL,'n');\n"
				err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
				want := uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(null, n) already exists.")
				referenceAssertDiagnostic(t, err, &want)
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(actualDump+"CREATE UNIQUE INDEX broken ON ui_t(;")); err == nil {
					t.Fatal("malformed restore accepted")
				}
				run(referenceSelect("SELECT k FROM ui_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
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
