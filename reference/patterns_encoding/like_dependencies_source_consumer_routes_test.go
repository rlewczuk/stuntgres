package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeStoredPatternEscapeDependencyRestrictionsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'a%','!'),(2,NULL,NULL)")
			fixture.StringCoreControlExec(t, db, "CREATE VIEW pattern_view AS SELECT id,'ab' LIKE p AS matched FROM t")
			fixture.StringCoreControlExec(t, db, "CREATE VIEW escape_view AS SELECT id,'a%b' LIKE 'a!%b' ESCAPE e AS matched FROM t")
			pattern, err := db.Prepare("SELECT id,matched FROM pattern_view ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer pattern.Close()
			escape, err := db.Prepare("SELECT id,matched FROM escape_view ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer escape.Close()
			views := func(t *testing.T) {
				t.Helper()
				for _, q := range []string{"SELECT id,matched FROM pattern_view ORDER BY id", "SELECT id,matched FROM escape_view ORDER BY id"} {
					rows, err := db.Query(q)
					fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}, false)
				}
				rows, err := pattern.Query()
				fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}, false)
				rows, err = escape.Query()
				fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}, false)
			}
			fixture.StringCoreControlExec(t, db, "ALTER TABLE t RENAME COLUMN p TO pattern")
			views(t)
			fixture.StringCoreControlExec(t, db, "ALTER TABLE t RENAME COLUMN e TO escape_value")
			views(t)
			checks := func(t *testing.T) {
				t.Helper()
				for _, tc := range []struct{ q, name, detail string }{
					{"INSERT INTO t VALUES(3,'b%','!')", "pattern_only", "3, b%, !"},
					{"INSERT INTO t VALUES(3,'a%','?')", "escape_only", "3, a%, ?"},
					{"UPDATE t SET pattern='b%' WHERE id=1", "pattern_only", "1, b%, !"},
					{"UPDATE t SET escape_value='?' WHERE id=1", "escape_only", "1, a%, ?"},
				} {
					want := fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "` + tc.name + `"`, Detail: "Failing row contains (" + tc.detail + ").", Schema: "public", Table: "t", Constraint: tc.name}
					_, err := db.Exec(tc.q)
					fixture.StringCoreControlDiagnostic(t, err, want)
					held, err := db.Prepare(tc.q)
					if err != nil {
						t.Fatal(err)
					}
					_, err = held.Exec()
					held.Close()
					fixture.StringCoreControlDiagnostic(t, err, want)
				}
				rows, err := db.Query("SELECT id,pattern,escape_value FROM t ORDER BY id")
				fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a%", "!"}, {int64(2), nil, nil}}, false)
			}
			dump := func(t *testing.T) []byte {
				t.Helper()
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
					t.Fatal(err)
				}
				return out.Bytes()
			}
			views(t)
			checks(t)
			var before []byte
			if !backend.PG {
				before = dump(t)
			}
			for _, tc := range []struct{ column, view string }{{"pattern", "pattern_view"}, {"escape_value", "escape_view"}} {
				t.Run(tc.column, func(t *testing.T) {
					q := "ALTER TABLE t DROP COLUMN " + tc.column + " RESTRICT"
					held, err := db.Prepare(q)
					if err != nil {
						t.Fatal("dependency restriction rejected Prepare", err)
					}
					defer held.Close()
					want := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column " + tc.column + " of table t because other objects depend on it", Detail: "view " + tc.view + " depends on column " + tc.column + " of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
					_, err = db.Exec(q)
					fixture.StringCoreControlDiagnostic(t, err, want)
					if !backend.PG && !bytes.Equal(before, dump(t)) {
						t.Fatal("failed direct DDL changed stored snapshot")
					}
					views(t)
					checks(t)
					_, err = held.Exec()
					fixture.StringCoreControlDiagnostic(t, err, want)
					if !backend.PG && !bytes.Equal(before, dump(t)) {
						t.Fatal("failed held DDL changed stored snapshot")
					}
					views(t)
					checks(t)
				})
			}
			// Own-table CHECK dependencies are automatic, unlike normal view edges.
			// Isolate them without adding unsupported DROP VIEW to the native surface.
			fixture.StringCoreControlExec(t, db, "CREATE TABLE check_t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))")
			fixture.StringCoreControlExec(t, db, "INSERT INTO check_t VALUES(1,'a%','!'),(2,NULL,NULL)")
			fixture.StringCoreControlExec(t, db, "ALTER TABLE check_t RENAME COLUMN p TO pattern")
			fixture.StringCoreControlExec(t, db, "ALTER TABLE check_t RENAME COLUMN e TO escape_value")
			fixture.StringCoreControlExec(t, db, "ALTER TABLE check_t DROP COLUMN pattern RESTRICT")
			_, err = db.Exec("INSERT INTO check_t VALUES(3,'?')")
			fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "23514", Message: `new row for relation "check_t" violates check constraint "escape_only"`, Detail: "Failing row contains (3, ?).", Schema: "public", Table: "check_t", Constraint: "escape_only"})
			fixture.StringCoreControlExec(t, db, "ALTER TABLE check_t DROP COLUMN escape_value RESTRICT")
			fixture.StringCoreControlExec(t, db, "INSERT INTO check_t VALUES(3)")
			rows, err := db.Query("SELECT id FROM check_t ORDER BY id")
			fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, false)
		})
	}
}
