package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeProviderControls(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SEQUENCE external START 5", 0))
		run(fixture.Exec("CREATE TABLE src(a INT DEFAULT 7,n BIGINT DEFAULT nextval('external'),CONSTRAINT positive CHECK(a>0),UNIQUE(a))", 0))
		run(fixture.Exec("CREATE INDEX source_hash ON src USING hash(n)", 0))
		run(fixture.Exec("CREATE TABLE clone(LIKE src INCLUDING ALL)", 0))
		run(fixture.Exec("DROP TABLE src", 0))
		run(fixture.Select("SELECT last_value,is_called FROM external", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), false}}))
		run(fixture.Exec("INSERT INTO clone DEFAULT VALUES", 1))
		want := fixture.Select("SELECT * FROM clone", []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(5)}})
		run(want)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT last_value,is_called FROM external", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), true}}))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO clone(a) VALUES(8)", 1))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT n FROM clone WHERE a=8", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
		run(fixture.Exec("INSERT INTO clone(a) VALUES(8)", 1))
		run(fixture.Select("SELECT n FROM clone WHERE a=8", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		run(fixture.TableLikeError("INSERT INTO clone(a,n) VALUES(-1,99)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "clone" violates check constraint "positive"`, Detail: "Failing row contains (-1, 99).", Schema: "public", Table: "clone", Constraint: "positive"}))
		run(fixture.TableLikeError("INSERT INTO clone(a,n) VALUES(7,99)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "clone_a_key"`, Detail: "Key (a)=(7) already exists.", Schema: "public", Table: "clone", Constraint: "clone_a_key"}))
		run(fixture.Select("SELECT a FROM clone WHERE n=6", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}}))
	})
}
