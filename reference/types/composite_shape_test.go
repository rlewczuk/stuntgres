package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeTableShape(t *testing.T) {
	cases := []struct {
		name, ddl, output, input string
		guarded                  bool
	}{
		{"append", `ALTER TABLE source ADD COLUMN extra text`, `(7,x,)`, `(8,y,z)`, false},
		{"append_null_default", `ALTER TABLE source ADD COLUMN extra text DEFAULT NULL`, `(7,x,)`, `(8,y,z)`, false},
		{"append_value_default", `ALTER TABLE source ADD COLUMN extra text DEFAULT 'z'`, `(7,x)`, `(8,y)`, true},
		{"append_not_null", `ALTER TABLE source ADD COLUMN extra text NOT NULL DEFAULT 'z'`, `(7,x)`, `(8,y)`, true},
		{"same_type", `ALTER TABLE source ALTER COLUMN id TYPE integer`, `(7,x)`, `(8,y)`, true},
		{"widen", `ALTER TABLE source ALTER COLUMN id TYPE bigint`, `(7,x)`, `(8,y)`, true},
		{"narrow", `ALTER TABLE source ALTER COLUMN id TYPE smallint`, `(7,x)`, `(8,y)`, true},
		{"text_to_varchar", `ALTER TABLE source ALTER COLUMN note TYPE varchar(8)`, `(7,x)`, `(8,y)`, true},
		{"drop", `ALTER TABLE source DROP COLUMN note`, `(7)`, `(8)`, false},
		{"rename_member", `ALTER TABLE source RENAME COLUMN note TO renamed`, `(7,x)`, `(8,y)`, false},
		{"not_null", `ALTER TABLE source ALTER COLUMN id SET NOT NULL`, `(7,x)`, `(8,y)`, false},
		{"check", `ALTER TABLE source ADD CONSTRAINT positive CHECK(id>0)`, `(7,x)`, `(8,y)`, false},
		{"default", `ALTER TABLE source ALTER COLUMN id SET DEFAULT 99`, `(7,x)`, `(8,y)`, false},
	}
	for _, container := range []string{"direct", "named", "domain"} {
		for _, tc := range cases {
			t.Run(container+"/"+tc.name, func(t *testing.T) {
				fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE source(id integer,note text)`)
					typeName := "source"
					initial := "(7,x)"
					output, input := tc.output, tc.input
					if container == "named" {
						fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE wrapped AS (r source)`)
						typeName = "wrapped"
						initial = `("(7,x)")`
						output = `("` + output + `")`
						input = `("` + input + `")`
					} else if container == "domain" {
						fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS source`)
						typeName = "wrapped"
					}
					fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+typeName+")")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,$1)`, 1, initial))
					if tc.guarded {
						want := fixture.CompositeReferenceDiagnostic("0A000", `cannot alter table "source" because column "holder.v" uses its row type`)
						fixture.CompositeReferenceError(t, p, c, tc.ddl, "execute", want)
					} else if tc.name == "check" && !b.PG {
						// ADD CHECK is an existing native DDL boundary, not a
						// prerequisite for executable composite values.
						want := fixture.CompositeReferenceDiagnostic("42601", `syntax error at or near "CHECK"`)
						want.Position = "44"
						fixture.CompositeReferenceError(t, p, c, tc.ddl, "prepare", want)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.ddl, 0))
					}
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{output}}, false)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES ($1,$2)`, 1, 2, input)))
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{output}, {input}}, true)
				})
			})
		}
	}
}

func TestReferenceCompositeHeldShape(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE base(id integer,note text)`)
				source := "base"
				if view {
					source = "source"
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE VIEW source AS SELECT id,note FROM base`)
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+source+")")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,x)'),(2,'(9,z)')`, 2))
				held, err := c.QueryContext(p.Ctx, `SELECT v FROM holder ORDER BY k`)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				fixture.EnumReferenceMetadata(b.PG, true)(t, held)
				// Use another physical connection while the eager result remains live.
				other, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				parameter, err := other.PrepareContext(p.Ctx, `INSERT INTO holder VALUES ($1,$2)`)
				if err != nil {
					t.Fatal(err)
				}
				defer parameter.Close()
				literal, err := other.PrepareContext(p.Ctx, `INSERT INTO holder VALUES (4,'(4,literal)')`)
				if err != nil {
					t.Fatal(err)
				}
				defer literal.Close()
				selection, err := other.PrepareContext(p.Ctx, `SELECT v FROM holder ORDER BY k`)
				if err != nil {
					t.Fatal(err)
				}
				defer selection.Close()
				ddl := `ALTER TABLE base ADD COLUMN extra text`
				if view {
					ddl = `CREATE OR REPLACE VIEW source AS SELECT id,note,id AS extra FROM base`
				}
				fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Exec(ddl, 0))
				for _, want := range []string{"(7,x)", "(9,z)"} {
					if !held.Next() {
						t.Fatal("missing held row", held.Err())
					}
					var got any
					if err := held.Scan(&got); err != nil {
						t.Fatal(err)
					}
					if fixture.Normalize(got) != want {
						t.Errorf("held row got %#v want %q", got, want)
					}
				}
				fixture.EnumReferenceMetadata(b.PG, true)(t, held)
				if held.Next() || held.Err() != nil {
					t.Fatal("held cursor tail", held.Err())
				}
				if result, err := parameter.ExecContext(p.Ctx, 3, "(3,parameter,)"); err != nil {
					t.Errorf("held parameter execute diagnostic=%#v", fixture.ExtractDiagnostic(t, err))
				} else if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Error(n, err)
				}
				if result, err := literal.ExecContext(p.Ctx); err != nil {
					t.Errorf("held literal execute diagnostic=%#v", fixture.ExtractDiagnostic(t, err))
				} else if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Error(n, err)
				}
				rows, err := selection.QueryContext(p.Ctx)
				if err != nil {
					t.Fatalf("held selection execute diagnostic=%#v", fixture.ExtractDiagnostic(t, err))
				}
				defer rows.Close()
				want := []string{"(7,x,)", "(9,z,)", "(3,parameter,)", "(4,literal,)"}
				for _, v := range want {
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					var got any
					if err := rows.Scan(&got); err != nil {
						t.Fatal(err)
					}
					if fixture.Normalize(got) != v {
						t.Errorf("held plan got %#v want %q", got, v)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("held plan tail", rows.Err())
				}
			})
		})
	}
}

func TestReferenceCompositeRenameAndMove(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE SCHEMA composite_move`, `CREATE TABLE base(id integer,note text)`)
				defer c.ExecContext(p.Ctx, `DROP SCHEMA composite_move CASCADE`)
				name, kind := "base", "TABLE"
				if view {
					name = "source"
					kind = "VIEW"
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE VIEW source AS SELECT id,note FROM base`)
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+name+")")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,x)')`, 1))
				fixture.CompositeReferenceSetup(t, p, b, c, "ALTER "+kind+" "+name+" RENAME COLUMN note TO renamed", "ALTER "+kind+" "+name+" RENAME TO moved", "ALTER "+kind+" moved SET SCHEMA composite_move")
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}}, true)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES (2,$1::composite_move.moved)`, 1, "(8,y)")))
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
			})
		})
	}
}
