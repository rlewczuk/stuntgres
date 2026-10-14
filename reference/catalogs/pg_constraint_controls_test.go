package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc_constraint_reader"})
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		setup := []string{"CREATE TABLE pc_control_parent(id INT PRIMARY KEY)", "CREATE TABLE pc_control_child(id BIGINT,b INT CONSTRAINT pc_control_nn NOT NULL,CONSTRAINT pc_control_ck CHECK(b>id),CONSTRAINT pc_control_u UNIQUE(b),CONSTRAINT pc_control_fk FOREIGN KEY(id) REFERENCES pc_control_parent,CONSTRAINT pc_control_x EXCLUDE USING btree(id WITH =))", "ALTER TABLE pc_control_child ADD CONSTRAINT pc_control_invalid CHECK(b<>0) NOT VALID"}
		pgClassExec(t, p, b, b.DB, setup...)
		check := func() {
			pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname,contype,conkey::text,convalidated,conenforced,conbin IS NOT NULL AS tree FROM pg_constraint WHERE conrelid='pc_control_child'::regclass::oid ORDER BY conname", []string{"conname", "contype", "conkey", "convalidated", "conenforced", "tree"}, []string{"NAME", "CHAR", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"pc_control_ck", "c", "{2,1}", true, true, true}, {"pc_control_fk", "f", "{1}", true, true, false}, {"pc_control_invalid", "c", "{2}", false, true, true}, {"pc_control_nn", "n", "{2}", true, true, false}, {"pc_control_u", "u", "{2}", true, true, false}, {"pc_control_x", "x", "{1}", true, true, false}}))
			pgConstraintFK(t, p, b, "pc_control_child", "pc_control_parent", "pc_control_fk", "{1}", "{1}", "{15}", "{96}", "{410}", "a", "a", "s", nil, true)
		}
		check()
		if !b.PG {
			retained, err := b.DB.QueryContext(p.Ctx, "SELECT conname,conkey,conpfeqop,conbin FROM pg_constraint WHERE conrelid='pc_control_child'::regclass::oid AND contype='f'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "INSERT INTO pg_constraint") {
				t.Fatal("not logical DDL", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_control_fk'", []string{"conname"}, []string{"NAME"}, nil))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			id := pgConstraintID(t, p, b.DB, "pc_control_child", "pc_control_fk")
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE pc_failed(a INT); INSERT INTO pc_failed VALUES('bad');")); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			check()
			if got := pgConstraintID(t, p, b.DB, "pc_control_child", "pc_control_fk"); got != id {
				t.Fatal("invalid Restore changed identity")
			}
			if !retained.Next() {
				t.Fatal("retained FK vanished", retained.Err())
			}
			var name, key, operators string
			var tree any
			if err := retained.Scan(&name, &key, &operators, &tree); err != nil {
				t.Fatal(err)
			}
			if name != "pc_control_fk" || key != "{1}" || operators != "{15}" || tree != nil {
				t.Fatal("retained control metadata", name, key, operators, tree)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained multiplicity", retained.Err())
			}
		} else {
			pgClassExec(t, p, b, b.DB, "DROP TABLE pc_control_child", "DROP TABLE pc_control_parent")
			pgClassExec(t, p, b, b.DB, setup...)
			check()
		}
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "CREATE TEMP TABLE pc_control_temp(a INT CONSTRAINT pc_control_temp_nn NOT NULL)")
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT conkey::text FROM pg_constraint WHERE conrelid='pc_control_temp'::regclass::oid", []string{"conkey"}, []string{"TEXT"}, [][]any{{"{1}"}}))
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		tempID := pgConstraintID(t, p, owner, "pc_control_temp", "pc_control_temp_nn")
		pgConstraintCheck(t, p, b, peer, fixture.Select("SELECT conname,conkey::text FROM pg_catalog.pg_constraint WHERE oid=$1", []string{"conname", "conkey"}, []string{"NAME", "TEXT"}, [][]any{{"pc_control_temp_nn", "{1}"}}, tempID))
		missing := fixture.ImplicitError("SELECT 'pc_control_temp'::regclass::oid", fixture.Diagnostic{Code: "42P01", Message: `relation "pc_control_temp" does not exist`, Position: "8"})
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, missing)
		pgClassExec(t, p, b, owner, "DROP TABLE pc_control_temp")
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_control_temp_nn'", []string{"conname"}, []string{"NAME"}, nil))
		pgClassExec(t, p, b, owner, "CREATE ROLE pc_constraint_reader", "SET ROLE pc_constraint_reader")
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT conname FROM pg_catalog.pg_constraint WHERE conname='pc_control_fk'", []string{"conname"}, []string{"NAME"}, [][]any{{"pc_control_fk"}}))
		pgClassExec(t, p, b, owner, "RESET ROLE", "DROP ROLE pc_constraint_reader", "CREATE TABLE public.pg_constraint(marker INT)", "SET search_path TO public,pg_catalog")
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT marker FROM pg_constraint", []string{"marker"}, []string{"INT4"}, nil))
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT conname FROM pg_catalog.pg_constraint WHERE conname='pc_control_fk'", []string{"conname"}, []string{"NAME"}, [][]any{{"pc_control_fk"}}))
	})
}
