package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceCompositeTypmodShape(t *testing.T) {
	for _, tc := range []struct {
		name, initial, ddl string
		guarded            bool
	}{
		{"varchar_widen", `note varchar(3)`, `ALTER TABLE source ALTER COLUMN note TYPE varchar(8)`, true},
		{"varchar_narrow", `note varchar(8)`, `ALTER TABLE source ALTER COLUMN note TYPE varchar(3)`, true},
		{"varchar_same", `note varchar(3)`, `ALTER TABLE source ALTER COLUMN note TYPE varchar(3)`, true},
		{"numeric_same", `note numeric(4,1)`, `ALTER TABLE source ALTER COLUMN note TYPE numeric(4,1)`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				compositeReferenceSetup(t, p, b, c, "CREATE TABLE source(id integer,"+tc.initial+")", `CREATE TABLE holder(k integer,v source)`)
				input := "(7,x)"
				if tc.name == "numeric_same" {
					input = "(7,1.0)"
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (1,$1)`, 1, input))
				if tc.guarded {
					compositeReferenceError(t, p, c, tc.ddl, "execute", compositeReferenceDiagnostic("0A000", `cannot alter table "source" because column "holder.v" uses its row type`))
				} else {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(tc.ddl, 0))
				}
				compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{input}}, false)
			})
		})
	}
}

func TestReferenceCompositeCharacterAssignment(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE named AS (i integer,t text)`, `CREATE TABLE strings(k integer,t text,v varchar(20),c char(10))`)
		for _, prepared := range []bool{false, true} {
			s := referenceExec(`INSERT INTO strings VALUES (1,'(7,x)'::named,'(7,x)'::named,'(7,x)'::named)`, 1)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT t,v,c FROM strings ORDER BY k`, []string{"t", "v", "c"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"(7,x)", "(7,x)", "(7,x)     "}}))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`DELETE FROM strings`, 1))
		}
	})
}

// Boundary discovery only: composite arrays remain identity-only natively.
// This measures recursive PG row-type guards, not new executable array scope.
func TestReferenceCompositeArrayShapeOracle(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		if !b.pg {
			return
		}
		compositeReferenceSetup(t, p, b, c, `CREATE TABLE source(id integer,note text)`, `CREATE TABLE holder(v source[])`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES ('{"(7,x)"}')`, 1))
		compositeReferenceError(t, p, c, `ALTER TABLE source ALTER COLUMN id TYPE bigint`, "execute", compositeReferenceDiagnostic("0A000", `cannot alter table "source" because column "holder.v" uses its row type`))
	})
}
