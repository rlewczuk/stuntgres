//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSchemaTableIntegration(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"tio"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "ti1", "ti2", "tin", "tid", "tif")
			step := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, q := range []string{
				"CREATE USER tio",
				"CREATE SCHEMA ti1 CREATE TABLE source(id INT NOT NULL)",
				"CREATE SCHEMA ti2 CREATE TABLE source(note TEXT)",
				"SET search_path TO ti2,ti1",
				"CREATE TABLE ti1.path_clone(LIKE source)",
				"CREATE TABLE ti2.qualified_clone(lead BIGINT,LIKE ti1.source,tail BOOL)",
			} {
				step(referenceExec(q, 0))
			}
			step(referenceExec("INSERT INTO ti1.source VALUES(7)", 1))
			step(referenceExec("INSERT INTO ti2.source VALUES('9')", 1))
			step(referenceSelect("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(referenceSelect("SELECT * FROM ti2.source", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
			// LIKE copies shape, never either source's rows.
			step(referenceSelect("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, nil))
			step(referenceSelect("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, nil))
			step(referenceExec("INSERT INTO ti1.path_clone VALUES('9')", 1))
			step(referenceExec("INSERT INTO ti2.qualified_clone VALUES(9,7,true)", 1))
			step(referenceExec("CREATE SCHEMA tin AUTHORIZATION tio CREATE TABLE source(id INT NOT NULL) CREATE TABLE clone(lead BOOL,LIKE source) CREATE TABLE z() CREATE TABLE zl(LIKE z)", 0))
			step(referenceSelect("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"postgres"}}))
			step(referenceSelect("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"ti2, ti1"}}))
			step(referenceSelect("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, nil))
			step(referenceSelect("SELECT * FROM tin.zl", []string{}, []string{}, nil))
			// The enclosing owner, not the bootstrap caller, must have source rights.
			// ti1 starts at character 61 in this literal statement on both backends.
			denied := roleReferenceError("CREATE SCHEMA tid AUTHORIZATION tio CREATE TABLE clone(LIKE ti1.source)", "42501", "permission denied for schema ti1", "", prepared, "execute")
			denied.diagnostic.Position = "61"
			p.step(t, denied)
			p.step(t, schemaReferenceCreationError("CREATE TABLE tid.probe(id INT)", "3F000", `schema "tid" does not exist`, prepared, "14"))
			p.step(t, roleReferenceError("CREATE SCHEMA tif CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source,id TEXT)", "42701", `column "id" specified more than once`, "", prepared, "execute"))
			p.step(t, schemaReferenceCreationError("CREATE TABLE tif.probe(id INT)", "3F000", `schema "tif" does not exist`, prepared, "14"))
			p.step(t, roleReferenceError("DROP TABLE ti1.source,ti2.absent", "42P01", `table "absent" does not exist`, "", prepared, "execute"))
			step(referenceSelect("SELECT * FROM ti1.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(referenceExec("DROP TABLE ti1.source RESTRICT", 0))
			step(referenceExec("INSERT INTO tin.clone VALUES(true,11)", 1))
			verify := func() {
				step(referenceSelect("SELECT * FROM ti1.path_clone", []string{"note"}, []string{"TEXT"}, [][]any{{"9"}}))
				step(referenceSelect("SELECT * FROM ti2.qualified_clone", []string{"lead", "id", "tail"}, []string{"INT8", "INT4", "BOOL"}, [][]any{{int64(9), int64(7), true}}))
				step(referenceSelect("SELECT * FROM tin.clone", []string{"lead", "id"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(11)}}))
				step(referenceSelect("SELECT * FROM tin.zl", []string{}, []string{}, nil))
				var owner string
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='tin' AND c.relname='clone'").Scan(&owner); err != nil || owner != "tio" {
					t.Fatal("nested/restored owner", owner, err)
				}
				var notnull bool
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='tin' AND c.relname='clone' AND a.attname='id'").Scan(&notnull); err != nil || !notnull {
					t.Fatal("nested/restored NOT NULL", notnull, err)
				}
			}
			verify()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "LIKE") || strings.Contains(dump.String(), `CREATE TABLE "ti1"."source"`) || !strings.Contains(dump.String(), `CREATE TABLE "tin"."zl" ();`) {
				t.Fatal("expanded independent qualified dump", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			oracle, err := referenceAdmin.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"DROP SCHEMA ti1 CASCADE", "DROP SCHEMA ti2 CASCADE", "DROP SCHEMA tin CASCADE"} {
				if _, err := oracle.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			// Replay the actual public native dump on pinned PostgreSQL, not an
			// independently reconstructed script. Namespace/table owners coincide.
			if _, err := oracle.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := oracle.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			verify()
		})
	}
}
