package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikeProviderControls(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE external START 5", 0))
		run(referenceExec("CREATE TABLE src(a INT DEFAULT 7,n BIGINT DEFAULT nextval('external'),CONSTRAINT positive CHECK(a>0),UNIQUE(a))", 0))
		run(referenceExec("CREATE INDEX source_hash ON src USING hash(n)", 0))
		run(referenceExec("CREATE TABLE clone(LIKE src INCLUDING ALL)", 0))
		run(referenceExec("DROP TABLE src", 0))
		run(referenceSelect("SELECT last_value,is_called FROM external", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), false}}))
		run(referenceExec("INSERT INTO clone DEFAULT VALUES", 1))
		want := referenceSelect("SELECT * FROM clone", []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(5)}})
		run(want)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT last_value,is_called FROM external", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), true}}))
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO clone(a) VALUES(8)", 1))
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT n FROM clone WHERE a=8", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
		run(referenceExec("INSERT INTO clone(a) VALUES(8)", 1))
		run(referenceSelect("SELECT n FROM clone WHERE a=8", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		run(tableLikeError("INSERT INTO clone(a,n) VALUES(-1,99)", referenceDiagnostic{Code: "23514", Message: `new row for relation "clone" violates check constraint "positive"`, Detail: "Failing row contains (-1, 99).", Schema: "public", Table: "clone", Constraint: "positive"}))
		run(tableLikeError("INSERT INTO clone(a,n) VALUES(7,99)", referenceDiagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "clone_a_key"`, Detail: "Key (a)=(7) already exists.", Schema: "public", Table: "clone", Constraint: "clone_a_key"}))
		run(referenceSelect("SELECT a FROM clone WHERE n=6", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}}))
	})
}
