package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceDependencyVisibilityConsumerRoutes(t *testing.T) {
	for _, tc := range []struct{ name, view, dependent, path, shadow, description, detail string }{
		{"moved", "v", "w", "public", "", "s.v", "w"},
		{"visible", "v", "w", "s,public", "", "v", "w"},
		{"shadowed", "v", "w", "public,s", "CREATE VIEW v AS SELECT id FROM t", "s.v", "w"},
		{"quoted", `"odd view"`, `"odd dependent"`, "public", "", `s."odd view"`, `"odd dependent"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s")
			for _, backend := range p.Backends() {
				db := backend.DB
				for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW s." + tc.view + " AS SELECT id FROM t", "CREATE VIEW " + tc.dependent + " AS SELECT id FROM s." + tc.view} {
					fixture.UpdateReturningReferenceExec(t, db, q)
				}
				if tc.shadow != "" {
					fixture.UpdateReturningReferenceExec(t, db, tc.shadow)
				}
				fixture.UpdateReturningReferenceExec(t, db, "SET search_path TO "+tc.path)
				_, err := db.Exec("DROP VIEW s." + tc.view)
				fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view " + tc.description + " because other objects depend on it", Detail: "view " + tc.detail + " depends on view " + tc.description, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				rows, err := db.Query("SELECT id FROM " + tc.dependent)
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
			}
		})
	}
}

func TestReferenceViewReplaceHiddenDependenciesConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE SEQUENCE old_seq", "CREATE SEQUENCE new_seq", "CREATE TABLE old_t(id INT,flag BOOL,txt TEXT)", "CREATE TABLE new_t(gone INT,id INT,flag BOOL,txt TEXT)", "CREATE TABLE old_join(id INT)", "CREATE TABLE new_join(id INT)", "INSERT INTO new_t VALUES(0,7,true,'x')", "INSERT INTO new_join VALUES(7)", "CREATE VIEW v AS SELECT nextval('old_seq') AS n FROM old_t JOIN old_join ON old_t.id=old_join.id WHERE flag ORDER BY txt", "CREATE OR REPLACE VIEW v AS SELECT nextval('new_seq') AS n FROM new_t JOIN new_join ON new_t.id=new_join.id WHERE flag ORDER BY txt", "DROP TABLE old_t", "DROP TABLE old_join", "DROP SEQUENCE old_seq", "ALTER TABLE new_t DROP COLUMN gone", "ALTER TABLE new_t RENAME COLUMN txt TO label", "ALTER TABLE new_join RENAME TO joined"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		rows, err := db.Query("SELECT last_value,is_called FROM new_seq")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"last_value", "is_called"}, Types: []string{"INT8", "BOOL"}, Rows: [][]any{{int64(1), false}}, Bag: false})
		for _, tc := range []struct{ query, message, detail string }{
			{"DROP SEQUENCE new_seq", "cannot drop sequence new_seq because other objects depend on it", "view v depends on sequence new_seq"},
			{"ALTER TABLE new_t DROP COLUMN flag", "cannot drop column flag of table new_t because other objects depend on it", "view v depends on column flag of table new_t"},
			{"ALTER TABLE new_t DROP COLUMN label", "cannot drop column label of table new_t because other objects depend on it", "view v depends on column label of table new_t"},
			{"DROP TABLE joined", "cannot drop table joined because other objects depend on it", "view v depends on table joined"},
		} {
			_, err := db.Exec(tc.query)
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "2BP01", Message: tc.message, Detail: tc.detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		}
		rows, err = db.Query("SELECT n FROM v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"n"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}}, Bag: false})
	}
}
