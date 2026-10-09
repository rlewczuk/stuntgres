package reference_test

import (
	"bytes"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterAddColumnControlsSmoke(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		// Native cursors are eager snapshots; PostgreSQL portals must close before AX.
		var consume func()
		if !backend.pg {
			held, err := p.native.QueryContext(p.ctx, "SELECT * FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			consume = func() {
				labels, err := held.Columns()
				if err != nil || !reflect.DeepEqual(labels, []string{"id"}) {
					t.Fatal(labels, err)
				}
				var values []int64
				for held.Next() {
					var id int64
					if err := held.Scan(&id); err != nil {
						t.Fatal(err)
					}
					values = append(values, id)
				}
				if err := held.Err(); err != nil || !reflect.DeepEqual(values, []int64{1, 2}) {
					t.Fatal(values, err)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		run(referenceExec("ALTER TABLE t ADD note TEXT", 0))
		run(referenceExec("ALTER TABLE t ADD n INT DEFAULT 7 NOT NULL CONSTRAINT positive CHECK(n>0)", 0))
		run(referenceExec("COMMENT ON COLUMN t.n IS 'added'", 0))
		run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
		run(referencePrepared(referenceExec("INSERT INTO t(id,n) VALUES(4,9)", 1)))
		want := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "note", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(7)}, {int64(2), nil, int64(7)}, {int64(3), nil, int64(7)}, {int64(4), nil, int64(9)}})
		run(want)
		if !backend.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{"DEFAULT", "positive", "NOT NULL", "added"} {
				if !strings.Contains(dump.String(), fragment) {
					t.Fatalf("missing %q in actual dump: %s", fragment, dump.String())
				}
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(referenceError("SELECT * FROM t", "42P01"))
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
			consume()
			for _, script := range []string{
				"CREATE TABLE bad(b INT DEFAULT 'oops')",
				"CREATE TABLE bad(b INT CHECK(missing>0))",
				"CREATE TABLE bad(b INT NOT NULL); INSERT INTO bad VALUES(1); INSERT INTO bad VALUES(NULL)",
			} {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid Restore succeeded", script)
				}
				run(want)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != dump.String() {
					t.Fatal("failed Restore changed sentinel", err, after.String())
				}
			}
			// Replay the exact native dump, not an independently rebuilt CREATE script.
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL dump replay", err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
				if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t"); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, want.query, want.columns, want.types, []bool{true, true, false}, 4)
		}
		run(referenceSelect("SELECT col_description('t'::regclass,3)", []string{"col_description"}, []string{"TEXT"}, [][]any{{"added"}}))
		run(referenceExec("INSERT INTO t(id,n) VALUES(5,DEFAULT)", 1))
		run(referenceSelect("SELECT n FROM t WHERE id=5", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceImplicitError("INSERT INTO t(id,n) VALUES(6,0)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (6, null, 0).", Schema: "public", Table: "t", Constraint: "positive"}))
		run(referenceImplicitError("INSERT INTO t(id,n) VALUES(6,NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (6, null, null).", Schema: "public", Table: "t", Column: "n"}))
	})
}
