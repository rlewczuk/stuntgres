package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintFeatureSmoke(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc_smoke_parent(id INT PRIMARY KEY)", "CREATE TABLE pc_smoke_child(id BIGINT,b INT CONSTRAINT pc_smoke_nn NOT NULL,CONSTRAINT pc_smoke_ck CHECK(b>id AND b<>0),CONSTRAINT pc_smoke_u UNIQUE(b),CONSTRAINT pc_smoke_fk FOREIGN KEY(id) REFERENCES pc_smoke_parent(id),CONSTRAINT pc_smoke_x EXCLUDE USING btree(id WITH =))")
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT contype,conkey::text FROM pg_constraint WHERE conrelid='pc_smoke_parent'::regclass::oid ORDER BY contype", []string{"contype", "conkey"}, []string{"CHAR", "TEXT"}, [][]any{{"n", "{1}"}, {"p", "{1}"}}))
		s := fixture.Select("SELECT * FROM pg_constraint WHERE FALSE", pgConstraintColumns, pgConstraintTypes, nil)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgConstraintMetadata(t, rows, b.PG) }
		pgConstraintCheck(t, p, b, b.DB, s)
		q := "SELECT conname,contype,conkey::text,conexclop::text,conenforced,convalidated,conbin IS NOT NULL AS tree FROM pg_constraint WHERE conrelid='pc_smoke_child'::regclass::oid ORDER BY conname"
		want := [][]any{{"pc_smoke_ck", "c", "{2,1}", nil, true, true, true}, {"pc_smoke_fk", "f", "{1}", nil, true, true, false}, {"pc_smoke_nn", "n", "{2}", nil, true, true, false}, {"pc_smoke_u", "u", "{2}", nil, true, true, false}, {"pc_smoke_x", "x", "{1}", "{410}", true, true, false}}
		pgConstraintCheck(t, p, b, b.DB, fixture.Select(q, []string{"conname", "contype", "conkey", "conexclop", "conenforced", "convalidated", "tree"}, []string{"NAME", "CHAR", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, want))
		pgConstraintFK(t, p, b, "pc_smoke_child", "pc_smoke_parent", "pc_smoke_fk", "{1}", "{1}", "{15}", "{96}", "{410}", "a", "a", "s", nil, true)
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='pc_smoke_child'::regclass::oid AND conname='pc_smoke_ck'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (((b > id) AND (b <> 0)))"}}))
		id := pgConstraintID(t, p, b.DB, "pc_smoke_child", "pc_smoke_fk")
		heldQ := "SELECT conname,conkey::text FROM pg_constraint WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, heldQ)
		check := func(name string) {
			pgConstraintCheck(t, p, b, held, fixture.Select(heldQ, []string{"conname", "conkey"}, []string{"NAME", "TEXT"}, [][]any{{name, "{1}"}}, id))
		}
		check("pc_smoke_fk")
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pc_smoke_child RENAME CONSTRAINT pc_smoke_fk TO pc_smoke_fk_new")
		check("pc_smoke_fk_new")
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "BEGIN", "ALTER TABLE pc_smoke_child ADD CONSTRAINT pc_smoke_extra CHECK(b<100)", "ALTER TABLE pc_smoke_child DROP CONSTRAINT pc_smoke_fk_new", "ROLLBACK")
		check("pc_smoke_fk_new")
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pc_smoke_child'::regclass::oid AND conname='pc_smoke_extra'", []string{"conname"}, []string{"NAME"}, nil))
	})
}
