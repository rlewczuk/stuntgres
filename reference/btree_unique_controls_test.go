package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUniqueBtreeControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
			run(referenceExec("CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
			run(referenceExec("ALTER TABLE t RENAME n TO key", 0))
			run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
			var dump bytes.Buffer
			if !b.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				const definition = `CREATE INDEX "idx" ON "public"."renamed" USING btree ("key" DESC NULLS LAST, "s" ASC NULLS FIRST);`
				if !strings.Contains(dump.String(), definition) {
					t.Fatal("missing current definition", dump.String())
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("INSERT INTO renamed VALUES(3,'c')", 1))
			run(referenceSelect("SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", []string{"key", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}}))
			if !b.pg {
				broken := dump.String() + "CREATE INDEX broken ON renamed(missing);\n"
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(broken)); err == nil {
					t.Fatal("invalid replay succeeded")
				}
			}
			run(referenceSelect("SELECT key FROM renamed ORDER BY key DESC NULLS LAST", []string{"key"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}))
			run(referenceSelect("SELECT relkind FROM pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
		})
	}
}

func TestReferenceUniqueBtreeDistinctOwnerControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"index_schema_owner", "index_table_owner"})
	uniqueReferenceSchemaCleanup(t, "s")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
			run(referenceExec("CREATE USER index_schema_owner", 0))
			run(referenceExec("CREATE USER index_table_owner", 0))
			const seed = `CREATE SCHEMA s AUTHORIZATION index_schema_owner; SET SESSION AUTHORIZATION index_table_owner; CREATE TABLE s.t(n INT,s TEXT); INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b'); CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST);`
			var dump bytes.Buffer
			if !b.pg {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(seed)); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{`CREATE SCHEMA "s" AUTHORIZATION "index_schema_owner";`, `SET SESSION AUTHORIZATION 'index_table_owner';`, `CREATE INDEX "idx" ON "s"."t" USING btree ("n" DESC NULLS LAST, "s" ASC NULLS FIRST);`} {
					if !strings.Contains(dump.String(), want) {
						t.Fatal(want, dump.String())
					}
				}
			} else {
				// Trusted native Restore bypasses SQL ACLs in isolated replay. PostgreSQL's
				// independent setup uses superuser creation followed by ownership transfer.
				run(referenceExec("CREATE SCHEMA s AUTHORIZATION index_schema_owner", 0))
				run(referenceExec("CREATE TABLE s.t(n INT,s TEXT)", 0))
				run(referenceExec("INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
				run(referenceExec("CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
				run(referenceExec("ALTER TABLE s.t OWNER TO index_table_owner", 0))
				run(referenceExec("GRANT USAGE ON SCHEMA s TO index_table_owner", 0))
			}
			for round := range 2 {
				if !b.pg {
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
				} else if round != 0 {
					run(referenceExec("DELETE FROM s.t WHERE n=3", 1))
				}
				sessionUser := "postgres"
				if b.pg {
					sessionUser = "stuntgres"
				}
				run(referenceSelect("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{sessionUser}}))
				run(referenceSelect("SELECT n,s FROM s.t ORDER BY n DESC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}, {nil, nil}}))
				run(referenceExec("SET SESSION AUTHORIZATION index_table_owner", 0))
				run(referenceImplicitError("CREATE INDEX denied ON s.t(n)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema s"}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceExec("INSERT INTO s.t VALUES(3,'c')", 1))
				run(referenceSelect("SELECT n FROM s.t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}))
			}
			run(referenceExec("DROP SCHEMA s CASCADE", 0))
			run(referenceExec("DROP ROLE index_table_owner", 0))
			run(referenceExec("DROP ROLE index_schema_owner", 0))
		})
	}
}

func TestReferenceUniqueBtreeHeldRowsAfterReset(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(2),(1)", 2))
			run(referenceExec("CREATE INDEX idx ON t(n)", 0))
			if b.pg {
				run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(referenceExec("DROP TABLE t", 0))
			} else {
				rows, err := b.db.QueryContext(p.ctx, "SELECT n FROM t ORDER BY n")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("CREATE TABLE t(s TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES('new')", 1))
				run(referenceExec("CREATE INDEX idx ON t(s)", 0))
				for _, want := range []int64{1, 2} {
					if !rows.Next() {
						t.Fatal("old eager row missing", rows.Err())
					}
					var got int64
					if err := rows.Scan(&got); err != nil || got != want {
						t.Fatal(got, want, err)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
			if b.pg {
				run(referenceExec("CREATE TABLE t(s TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES('new')", 1))
				run(referenceExec("CREATE INDEX idx ON t(s)", 0))
			}
			run(referenceSelect("SELECT s FROM t", []string{"s"}, []string{"TEXT"}, [][]any{{"new"}}))
		})
	}
}

func TestReferenceUniqueBtreeRestoreRetainedTemporaryCollision(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "portable")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			retained, _ := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			local := func(s referenceStep) { referenceRunStep(t, p.ctx, retained, b.recorder, s) }
			local(referenceExec("CREATE TEMP SEQUENCE retained CACHE 10", 0))
			local(referenceSelect("SELECT nextval('retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			const stream = `CREATE SCHEMA portable; CREATE TABLE portable.owner_table(v INT); INSERT INTO portable.owner_table VALUES(7); CREATE INDEX owner_idx ON portable.owner_table(v);`
			if !b.pg {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream)); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, q := range []string{"CREATE SCHEMA portable", "CREATE TABLE portable.owner_table(v INT)", "INSERT INTO portable.owner_table VALUES(7)", "CREATE INDEX owner_idx ON portable.owner_table(v)"} {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						count = 1
					}
					run(referenceExec(q, count))
				}
			}
			local(referenceSelect("SELECT nextval('retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceExec("INSERT INTO portable.owner_table VALUES(8)", 1))
			run(referenceSelect("SELECT v FROM portable.owner_table ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				const definition = `CREATE INDEX "owner_idx" ON "portable"."owner_table" USING btree ("v" ASC NULLS LAST);`
				if !strings.Contains(dump.String(), definition) || strings.Contains(dump.String(), "CREATE TEMP") {
					t.Fatal(dump.String())
				}
			}
			run(referenceExec("DROP TABLE portable.owner_table", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='owner_idx'", []string{"relname"}, []string{"NAME"}, nil))
			local(referenceSelect("SELECT nextval('retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(referenceExec("DROP SCHEMA portable", 0))
		})
	}
}
