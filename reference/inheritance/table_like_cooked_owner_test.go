package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeCookedOwner(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(dead TEXT,a INT,g OID GENERATED ALWAYS AS(tableoid) STORED,r REGCLASS,d TIMESTAMPTZ DEFAULT 'now')", 0))
		run(fixture.Exec("ALTER TABLE src ALTER COLUMN r SET DEFAULT 'src'::regclass", 0))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN dead", 0))
		run(fixture.Exec("INSERT INTO src(a) VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING DEFAULTS INCLUDING GENERATED)", 0))
		run(fixture.Exec("INSERT INTO clone(a) VALUES(2)", 1))
		witness := fixture.Select("SELECT clone.g='clone'::regclass::oid AS owner,clone.r='src'::regclass AS literal,clone.d=src.d AS frozen FROM clone,src", []string{"owner", "literal", "frozen"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
		run(witness)
		if !b.PG {
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
			run(witness)
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, witness)
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
