package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSchemaTableIntegration(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
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
		clean(b.db, b.pg)
		defer clean(b.db, b.pg)
		run(referenceExec("CREATE USER tio", 0))
		nativeRoleCreated = !b.pg
		for _, q := range []string{
			"CREATE SCHEMA ti1 CREATE TABLE source(id INT NOT NULL)",
			"CREATE SCHEMA ti2 CREATE TABLE source(note TEXT)",
			"SET search_path TO ti2,ti1",
			"CREATE TABLE ti1.path_clone(LIKE source)",
			"CREATE TABLE ti2.qualified_clone(lead BIGINT,LIKE ti1.source,tail BOOL)",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO ti1.source VALUES(7)", 1))
		run(referenceExec("INSERT INTO ti2.source VALUES('9')", 1))
		run(referenceSelect("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceSelect("SELECT * FROM ti2.source", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
		run(referenceExec("INSERT INTO ti2.source VALUES('nine')", 1))
		run(referenceSelect("SELECT * FROM ti2.source ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"nine"}}))
		run(referenceSelect("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, nil))
		run(referenceSelect("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, nil))
		run(referenceExec("INSERT INTO ti1.path_clone VALUES('9')", 1))
		run(referenceSelect("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
		run(referenceExec("INSERT INTO ti1.path_clone VALUES('path')", 1))
		run(referenceSelect("SELECT * FROM ti1.path_clone ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"path"}}))
		run(referenceExec("INSERT INTO ti2.qualified_clone VALUES(9,7,true)", 1))
		run(referenceExec("CREATE SCHEMA tin AUTHORIZATION tio CREATE TABLE source(id INT NOT NULL) CREATE TABLE clone(lead BOOL,LIKE source) CREATE TABLE z() CREATE TABLE zl(LIKE z)", 0))
		actor := "postgres"
		if b.pg {
			actor = "stuntgres"
		}
		run(referenceSelect("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{actor}}))
		run(referenceSelect("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"ti2, ti1"}}))
		run(referenceSelect("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, nil))
		run(referenceSelect("SELECT * FROM tin.zl", []string{}, []string{}, nil))
		run(schemaReferenceCreationError("CREATE SCHEMA tid AUTHORIZATION tio CREATE TABLE clone(LIKE ti1.source)", "42501", "permission denied for schema ti1", prepared, "61"))
		run(schemaReferenceCreationError("CREATE TABLE tid.probe(id INT)", "3F000", `schema "tid" does not exist`, prepared, "14"))
		run(roleReferenceError("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source,id TEXT)", "42701", `column "id" specified more than once`, "", prepared, "execute"))
		run(schemaReferenceCreationError("CREATE TABLE tif.probe(id INT)", "3F000", `schema "tif" does not exist`, prepared, "14"))
		run(schemaReferenceCreationError("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(id absent_type)", "42704", `type "absent_type" does not exist`, prepared, "69"))
		run(schemaReferenceCreationError("CREATE TABLE tif.probe(id INT)", "3F000", `schema "tif" does not exist`, prepared, "14"))
		run(referenceExec("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source INCLUDING ALL)", 0))
		run(referenceSelect("SELECT * FROM tif.clone", []string{"id"}, []string{"INT4"}, nil))
		run(roleReferenceError("DROP TABLE ti1.source,ti2.absent", "42P01", `table "absent" does not exist`, "", prepared, "execute"))
		run(referenceSelect("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("DROP TABLE ti1.source,ti1.source RESTRICT", 0))
		run(referenceExec("CREATE TABLE tin.other(id INT)", 0))
		run(referenceExec("SET SESSION AUTHORIZATION tio", 0))
		run(referenceExec("DROP TABLE tin.other", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("INSERT INTO tin.clone VALUES(true,11)", 1))
		checks := []referenceStep{
			referenceSelect("SELECT * FROM ti1.path_clone ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}, {"path"}}),
			referenceSelect("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, [][]any{{int64(9), int64(7), true}}),
			referenceSelect("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(11)}}),
			referenceSelect("SELECT * FROM tin.zl", []string{}, []string{}, nil),
			referenceSelect("SELECT * FROM tin.z", []string{}, []string{}, nil),
			referenceSelect("SELECT * FROM tif.clone", []string{"id"}, []string{"INT4"}, nil),
		}
		metadata := []referenceStep{
			referenceSelect("SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='tin' AND c.relname='clone'", []string{"rolname"}, []string{"NAME"}, [][]any{{"tio"}}),
			referenceSelect("SELECT a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='tin' AND c.relname='clone' AND a.attname='id'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
		}
		verify := func() {
			for _, s := range checks {
				run(s)
			}
			if b.pg {
				for _, s := range metadata {
					run(s)
				}
			}
		}
		verify()
		if b.pg {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify()
		if p.postgres != nil {
			clean(p.postgres, true)
			defer clean(p.postgres, true)
			oracle, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := oracle.ExecContext(p.ctx, "CREATE USER tio"); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			for _, s := range checks {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, oracle, p.postgresRecord, s)
			}
			for _, s := range metadata {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, oracle, p.postgresRecord, s)
			}
		}
	})
}
