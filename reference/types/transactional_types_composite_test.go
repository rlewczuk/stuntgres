package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesComposite(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "private"}[private], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					if private {
						run(fixture.Exec("BEGIN", 0))
					}
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					for _, q := range []string{"CREATE TYPE tt_pair AS(id integer,note text)", "CREATE TABLE tt_parent OF tt_pair", "CREATE TABLE tt_left() INHERITS(tt_parent)", "CREATE TABLE tt_right() INHERITS(tt_parent)", "CREATE TABLE tt_bottom() INHERITS(tt_left,tt_right)"} {
						run(fixture.Exec(q, 0))
					}
					if !private {
						run(fixture.Exec("BEGIN", 0))
					}
					run(fixture.Exec("INSERT INTO tt_parent VALUES(7,'parent')", 1))
					run(fixture.Exec("INSERT INTO tt_bottom VALUES(40000,'own')", 1))
					run(fixture.Exec("SAVEPOINT original", 0))
					d := fixture.ControlsReferenceDiagnostic("2BP01", `cannot alter type "tt_pair" because it is the type of a typed table`)
					d.Hint = "Use ALTER ... CASCADE to alter the typed tables too."
					run(fixture.ErrorDiagnostic("ALTER TYPE tt_pair ADD ATTRIBUTE extra integer RESTRICT", d))
					run(fixture.Exec("ROLLBACK TO original", 0))
					run(fixture.Exec("ALTER TYPE tt_pair ADD ATTRIBUTE extra integer CASCADE", 0))
					run(fixture.Select("SELECT id,note,extra FROM tt_parent ORDER BY id", []string{"id", "note", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "parent", nil}, {int64(40000), "own", nil}}))
					run(fixture.Exec("UPDATE tt_bottom SET extra=19", 1))
					run(fixture.Exec("ALTER TYPE tt_pair RENAME ATTRIBUTE extra TO renamed CASCADE", 0))
					run(fixture.Select("SELECT renamed FROM tt_bottom", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(19)}}))
					run(fixture.Exec("ALTER TYPE tt_pair DROP ATTRIBUTE renamed CASCADE", 0))
					run(fixture.Exec("ALTER TYPE tt_pair ADD ATTRIBUTE renamed integer CASCADE", 0))
					run(fixture.Select("SELECT renamed FROM tt_bottom", []string{"renamed"}, []string{"INT4"}, [][]any{{nil}}))
					run(fixture.Exec("ALTER TYPE tt_pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
					wide := fixture.Select("SELECT id,note,renamed FROM tt_parent ORDER BY id", []string{"id", "note", "renamed"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(7), "parent", nil}, {int64(40000), "own", nil}})
					run(wide)
					run(fixture.Exec("SAVEPOINT wide", 0))
					run(fixture.ErrorDiagnostic("ALTER TYPE tt_pair ADD ATTRIBUTE late boolean CASCADE, ALTER ATTRIBUTE id TYPE smallint CASCADE", fixture.ControlsReferenceDiagnostic("22003", "smallint out of range")))
					run(fixture.Exec("ROLLBACK TO wide", 0))
					run(wide)
					run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='tt_bottom'::regclass AND attname='late' AND NOT attisdropped", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("ROLLBACK TO original", 0))
					run(fixture.Exec("ROLLBACK TO original", 0))
					run(fixture.Select("SELECT id,note FROM tt_parent ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "parent"}, {int64(40000), "own"}}))
					run(fixture.Exec("RELEASE original", 0))
					run(fixture.Exec("ROLLBACK", 0))
					if !private {
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,note FROM tt_parent", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
					}
				})
			}
		})
	}
	t.Run("stored_values", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE TYPE tt_pair AS(id integer,note text)", "CREATE DOMAIN tt_box AS tt_pair", "CREATE TYPE tt_outer AS(p tt_pair)", "CREATE TABLE tt_holder(p tt_box,a tt_pair[],n tt_outer)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO tt_holder VALUES('(7,x)','[0:1]={\"(7,x)\",NULL}','(\"(7,x)\")')", 1))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("ALTER TYPE tt_pair ADD ATTRIBUTE extra integer", 0))
				run(fixture.Select("SELECT (p).id,(p).extra,array_lower(a,1),array_upper(a,1),(a[0]).id,(a[0]).extra,(n).p.id FROM tt_holder", []string{"id", "extra", "array_lower", "array_upper", "id", "extra", "id"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(7), nil, int64(0), int64(1), int64(7), nil, int64(7)}}))
				run(fixture.Exec("SAVEPOINT stored", 0))
				// Keep the array as the sole stored blocker, after proving all carriers.
				run(fixture.Exec("ALTER TABLE tt_holder DROP COLUMN p, DROP COLUMN n", 0))
				d := fixture.ControlsReferenceDiagnostic("0A000", `cannot alter type "tt_pair" because column "tt_holder.a" uses it`)
				run(fixture.ErrorDiagnostic("ALTER TYPE tt_pair ALTER ATTRIBUTE id TYPE integer CASCADE", d))
				run(fixture.Exec("ROLLBACK TO stored", 0))
				run(fixture.Select("SELECT (p).id,(a[0]).id,(n).p.id FROM tt_holder", []string{"id", "id", "id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(7)}}))
				run(fixture.Exec("ROLLBACK", 0))
			})
		}
	})
}

func TestReferenceTransactionalTypesKeys(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "private"}[private], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					if private {
						run(fixture.Exec("BEGIN", 0))
					}
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					for _, q := range []string{"CREATE TYPE tt_pair AS(id integer,note text)", "CREATE TABLE tt_keys OF tt_pair(PRIMARY KEY(id), UNIQUE(note))"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO tt_keys VALUES(1,'a'),(2,'b')", 2))
					if !private {
						run(fixture.Exec("BEGIN", 0))
					}
					run(fixture.Exec("UPDATE tt_keys SET id=3,note='c' WHERE id=2", 1))
					run(fixture.Exec("DELETE FROM tt_keys WHERE id=1", 1))
					run(fixture.Exec("INSERT INTO tt_keys VALUES(1,'a')", 1))
					run(fixture.Exec("ALTER TYPE tt_pair ADD ATTRIBUTE extra integer CASCADE", 0))
					run(fixture.Exec("ALTER TYPE tt_pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
					run(fixture.Exec("SAVEPOINT keys", 0))
					d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "tt_keys_pkey"`)
					d.Detail = "Key (id)=(3) already exists."
					d.Schema, d.Table, d.Constraint = "public", "tt_keys", "tt_keys_pkey"
					run(fixture.ErrorDiagnostic("INSERT INTO tt_keys(id,note) VALUES(3,'d')", d))
					run(fixture.Exec("ROLLBACK TO keys", 0))
					run(fixture.Exec("INSERT INTO tt_keys(id,note) VALUES(2,'b')", 1))
					run(fixture.Select("SELECT id,note,extra FROM tt_keys ORDER BY id", []string{"id", "note", "extra"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(1), "a", nil}, {int64(2), "b", nil}, {int64(3), "c", nil}}))
					run(fixture.Exec("ROLLBACK", 0))
				})
			}
		})
	}
}
