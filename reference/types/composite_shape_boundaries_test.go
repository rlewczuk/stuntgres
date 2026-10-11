package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
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
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE source(id integer,"+tc.initial+")", `CREATE TABLE holder(k integer,v source)`)
				input := "(7,x)"
				if tc.name == "numeric_same" {
					input = "(7,1.0)"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,$1)`, 1, input))
				if tc.guarded {
					fixture.CompositeReferenceError(t, p, c, tc.ddl, "execute", fixture.CompositeReferenceDiagnostic("0A000", `cannot alter table "source" because column "holder.v" uses its row type`))
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.ddl, 0))
				}
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{input}}, false)
			})
		})
	}
}

func TestReferenceCompositeCharacterAssignment(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE named AS (i integer,t text)`, `CREATE TABLE strings(k integer,t text,v varchar(20),c char(10))`)
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(`INSERT INTO strings VALUES (1,'(7,x)'::named,'(7,x)'::named,'(7,x)'::named)`, 1)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT t,v,c FROM strings ORDER BY k`, []string{"t", "v", "c"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"(7,x)", "(7,x)", "(7,x)     "}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM strings`, 1))
		}
	})
}

// Boundary discovery only: composite arrays remain identity-only natively.
// This measures recursive PG row-type guards, not new executable array scope.
func TestReferenceCompositeArrayShapeOracle(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG {
			return
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE source(id integer,note text)`, `CREATE TABLE holder(v source[])`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES ('{"(7,x)"}')`, 1))
		fixture.CompositeReferenceError(t, p, c, `ALTER TABLE source ALTER COLUMN id TYPE bigint`, "execute", fixture.CompositeReferenceDiagnostic("0A000", `cannot alter table "source" because column "holder.v" uses its row type`))
	})
}
