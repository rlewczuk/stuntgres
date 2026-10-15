package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// This separate oracle-first witness pins admitted expression transport without
// enabling explicit bit casts or changing the completed operator scenarios.
func TestReferenceBitOperatorsStoredNullFamilies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			transport := fixture.Select("SELECT B'' OPERATOR(pg_catalog.&) NULL AS f,B'' OPERATOR(pg_catalog.||) NULL AS v,B'01' OPERATOR(pg_catalog.||) B'' AS w", []string{"f", "v", "w"}, []string{"BIT", "VARBIT", "VARBIT"}, [][]any{{nil, nil, "01"}})
			transport.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				bitOperatorsMetadata(t, cols, transport.Columns, transport.Types)
			}
			bitOperatorsBoth(t, p, b, c, transport)
			run(fixture.Exec(`CREATE TABLE bop_null_families(id INTEGER,f "bit" DEFAULT (B'' OPERATOR(pg_catalog.&) NULL),v VARBIT DEFAULT (B'' OPERATOR(pg_catalog.||) NULL),w VARBIT DEFAULT (B'01' OPERATOR(pg_catalog.||) B''))`, 0))
			run(fixture.Exec("CREATE VIEW bop_null_family_view AS SELECT id,~f AS nf,f<<1 AS shifted,v||w AS cat,w||B'' AS w,w='01' AS eq FROM bop_null_families", 0))
			run(fixture.Exec("INSERT INTO bop_null_families(id) VALUES(1)", 1))
			table := func(id int) fixture.Step {
				s := fixture.Select("SELECT f,v,w FROM bop_null_families WHERE id=$1", []string{"f", "v", "w"}, []string{"BIT", "VARBIT", "VARBIT"}, [][]any{{nil, nil, "01"}}, id)
				s.CheckMetadata = bitstringReferenceMetadata(b.PG, []int64{-1, -1, -1}, []bool{true, true, true})
				return s
			}
			view := func(pg bool, id int) fixture.Step {
				s := fixture.Select("SELECT nf,shifted,cat,w,eq FROM bop_null_family_view WHERE id=$1", []string{"nf", "shifted", "cat", "w", "eq"}, []string{"BIT", "BIT", "VARBIT", "VARBIT", "BOOL"}, [][]any{{nil, nil, nil, "01", true}}, id)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					bitOperatorsMetadata(t, cols, s.Columns, s.Types, !pg)
				}
				return s
			}
			bitOperatorsBoth(t, p, b, c, table(1))
			bitOperatorsBoth(t, p, b, c, view(b.PG, 1))
			var dump bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("unstable typed-family replay", err, redump.String())
				}
				bitOperatorsBoth(t, p, b, c, table(1))
				bitOperatorsBoth(t, p, b, c, view(b.PG, 1))
			}
			run(fixture.Exec("INSERT INTO bop_null_families(id) VALUES(2)", 1))
			bitOperatorsBoth(t, p, b, c, table(2))
			bitOperatorsBoth(t, p, b, c, view(b.PG, 2))
			if !b.PG && p.Postgres != nil {
				var pg fixture.Backend
				for _, candidate := range p.Backends() {
					if candidate.PG {
						pg = candidate
					}
				}
				for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public", dump.String()} {
					if _, err := pg.DB.ExecContext(p.Ctx, q); err != nil {
						t.Fatal("actual native typed-family dump replay", err, q)
					}
				}
				// Reuse the same independent row/type contracts on the emitted SQL.
				for _, id := range []int{1, 2} {
					if id == 2 {
						fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Exec("INSERT INTO bop_null_families(id) VALUES(2)", 1))
					}
					s := table(id)
					s.CheckMetadata = bitstringReferenceMetadata(true, []int64{-1, -1, -1}, nil)
					bitOperatorsBoth(t, p, pg, pg.DB, s)
					bitOperatorsBoth(t, p, pg, pg.DB, view(true, id))
				}
				for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := pg.DB.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
