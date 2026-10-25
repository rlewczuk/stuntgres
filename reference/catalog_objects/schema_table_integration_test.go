package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaTableIntegration(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		nativeRoleCreated := false
		clean := func(db *sql.DB, pg bool) {
			for _, name := range []string{"ti1", "ti2", "tin", "tid", "tif"} {
				if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
					t.Fatal(err)
				}
			}
			if pg || nativeRoleCreated {
				query := "DROP ROLE tio"
				if pg {
					query = "DROP ROLE IF EXISTS tio"
				}
				if _, err := db.ExecContext(context.Background(), query); err != nil {
					t.Fatal(err)
				}
				if !pg {
					nativeRoleCreated = false
				}
			}
		}
		clean(b.DB, b.PG)
		defer clean(b.DB, b.PG)
		run(fixture.Exec("CREATE USER tio", 0))
		nativeRoleCreated = !b.PG
		for _, q := range []string{
			"CREATE SCHEMA ti1 CREATE TABLE source(id INT NOT NULL)",
			"CREATE SCHEMA ti2 CREATE TABLE source(note TEXT)",
			"SET search_path TO ti2,ti1",
			"CREATE TABLE ti1.path_clone(LIKE source)",
			"CREATE TABLE ti2.qualified_clone(lead BIGINT,LIKE ti1.source,tail BOOL)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ti1.source VALUES(7)", 1))
		run(fixture.Exec("INSERT INTO ti2.source VALUES('9')", 1))
		run(fixture.Select("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Select("SELECT * FROM ti2.source", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
		run(fixture.Exec("INSERT INTO ti2.source VALUES('nine')", 1))
		run(fixture.Select("SELECT * FROM ti2.source ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"nine"}}))
		run(fixture.Select("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, nil))
		run(fixture.Select("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, nil))
		run(fixture.Exec("INSERT INTO ti1.path_clone VALUES('9')", 1))
		run(fixture.Select("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
		run(fixture.Exec("INSERT INTO ti1.path_clone VALUES('path')", 1))
		run(fixture.Select("SELECT * FROM ti1.path_clone ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"path"}}))
		run(fixture.Exec("INSERT INTO ti2.qualified_clone VALUES(9,7,true)", 1))
		run(fixture.Exec("CREATE SCHEMA tin AUTHORIZATION tio CREATE TABLE source(id INT NOT NULL) CREATE TABLE clone(lead BOOL,LIKE source) CREATE TABLE z() CREATE TABLE zl(LIKE z)", 0))
		actor := "postgres"
		if b.PG {
			actor = "stuntgres"
		}
		run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{actor}}))
		run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"ti2, ti1"}}))
		run(fixture.Select("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, nil))
		run(fixture.Select("SELECT * FROM tin.zl", []string{}, []string{}, nil))
		run(fixture.SchemaReferenceCreationError("CREATE SCHEMA tid AUTHORIZATION tio CREATE TABLE clone(LIKE ti1.source)", "42501", "permission denied for schema ti1", prepared, "61"))
		run(fixture.SchemaReferenceCreationError("CREATE TABLE tid.probe(id INT)", "3F000", `schema "tid" does not exist`, prepared, "14"))
		run(fixture.RoleReferenceError("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source,id TEXT)", "42701", `column "id" specified more than once`, "", prepared, "execute"))
		run(fixture.SchemaReferenceCreationError("CREATE TABLE tif.probe(id INT)", "3F000", `schema "tif" does not exist`, prepared, "14"))
		run(fixture.SchemaReferenceCreationError("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(id absent_type)", "42704", `type "absent_type" does not exist`, prepared, "69"))
		run(fixture.SchemaReferenceCreationError("CREATE TABLE tif.probe(id INT)", "3F000", `schema "tif" does not exist`, prepared, "14"))
		run(fixture.Exec("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source INCLUDING ALL)", 0))
		run(fixture.Select("SELECT * FROM tif.clone", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.RoleReferenceError("DROP TABLE ti1.source,ti2.absent", "42P01", `table "absent" does not exist`, "", prepared, "execute"))
		run(fixture.Select("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("DROP TABLE ti1.source,ti1.source RESTRICT", 0))
		run(fixture.Exec("CREATE TABLE tin.other(id INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION tio", 0))
		run(fixture.Exec("DROP TABLE tin.other", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO tin.clone VALUES(true,11)", 1))
		checks := []fixture.Step{
			fixture.Select("SELECT * FROM ti1.path_clone ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"path"}}),
			fixture.Select("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, [][]any{{int64(9), int64(7), true}}),
			fixture.Select("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(11)}}),
			fixture.Select("SELECT * FROM tin.zl", []string{}, []string{}, nil),
			fixture.Select("SELECT * FROM tin.z", []string{}, []string{}, nil),
			fixture.Select("SELECT * FROM tif.clone", []string{"id"}, []string{"INT4"}, nil),
		}
		metadata := []fixture.Step{
			fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='tin' AND c.relname='clone'", []string{"rolname"}, []string{"NAME"}, [][]any{{"tio"}}),
			fixture.Select("SELECT a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='tin' AND c.relname='clone' AND a.attname='id'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
		}
		verify := func() {
			for _, s := range checks {
				run(s)
			}
			if b.PG {
				for _, s := range metadata {
					run(s)
				}
			}
		}
		verify()
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify()
		if p.Postgres != nil {
			clean(p.Postgres, true)
			defer clean(p.Postgres, true)
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := oracle.ExecContext(p.Ctx, "CREATE USER tio"); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			for _, s := range checks {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, s)
			}
			for _, s := range metadata {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, s)
			}
		}
	})
}
