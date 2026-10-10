package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikeCookedOwner(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(dead TEXT,a INT,g OID GENERATED ALWAYS AS(tableoid) STORED,r REGCLASS,d TIMESTAMPTZ DEFAULT 'now')", 0))
		run(referenceExec("ALTER TABLE src ALTER COLUMN r SET DEFAULT 'src'::regclass", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN dead", 0))
		run(referenceExec("INSERT INTO src(a) VALUES(1)", 1))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING DEFAULTS INCLUDING GENERATED)", 0))
		run(referenceExec("INSERT INTO clone(a) VALUES(2)", 1))
		witness := referenceSelect("SELECT clone.g='clone'::regclass::oid AS owner,clone.r='src'::regclass AS literal,clone.d=src.d AS frozen FROM clone,src", []string{"owner", "literal", "frozen"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
		run(witness)
		if !b.pg {
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
			run(witness)
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, witness)
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
