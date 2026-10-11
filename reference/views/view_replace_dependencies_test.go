package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceHiddenDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE SEQUENCE old_seq", "CREATE SEQUENCE new_seq", "CREATE TABLE old_t(id INT,flag BOOL,txt TEXT)", "CREATE TABLE new_t(gone INT,id INT,flag BOOL,txt TEXT)", "CREATE TABLE old_join(id INT)", "CREATE TABLE new_join(id INT)", "INSERT INTO new_t VALUES(0,7,true,'x')", "INSERT INTO new_join VALUES(7)", "CREATE VIEW v AS SELECT nextval('old_seq') AS n FROM old_t JOIN old_join ON old_t.id=old_join.id WHERE flag ORDER BY txt", "CREATE OR REPLACE VIEW v AS SELECT nextval('new_seq') AS n FROM new_t JOIN new_join ON new_t.id=new_join.id WHERE flag ORDER BY txt", "DROP TABLE old_t", "DROP TABLE old_join", "DROP SEQUENCE old_seq", "ALTER TABLE new_t DROP COLUMN gone", "ALTER TABLE new_t RENAME COLUMN txt TO label", "ALTER TABLE new_join RENAME TO joined"} {
		count := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			count = 1
		}
		p.Step(t, fixture.Exec(q, count))
	}
	p.Step(t, fixture.Select("SELECT last_value,is_called FROM new_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
	for _, tc := range []struct{ query, message, detail string }{
		{"DROP SEQUENCE new_seq", "cannot drop sequence new_seq because other objects depend on it", "view v depends on sequence new_seq"},
		{"ALTER TABLE new_t DROP COLUMN flag", "cannot drop column flag of table new_t because other objects depend on it", "view v depends on column flag of table new_t"},
		{"ALTER TABLE new_t DROP COLUMN label", "cannot drop column label of table new_t because other objects depend on it", "view v depends on column label of table new_t"},
		{"DROP TABLE joined", "cannot drop table joined because other objects depend on it", "view v depends on table joined"},
	} {
		p.Step(t, fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "2BP01", Message: tc.message, Detail: tc.detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
	}
	p.Step(t, fixture.Select("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
}
