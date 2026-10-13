package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateMetadataCases(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	for _, tc := range []struct{ tag, input, typ, values string; row []any }{
		{"s", "SMALLINT", "INT2", "(12),(10),(3)", []any{int64(0),int64(15),int64(5)}},
		{"i", "INTEGER", "INT4", "(12),(10),(3)", []any{int64(0),int64(15),int64(5)}},
		{"b", "BIGINT", "INT8", "(12),(10),(3)", []any{int64(0),int64(15),int64(5)}},
		{"f", "BIT(5)", "BIT", "(B'10101'),(B'11000'),(B'00111')", []any{"00000","11111","01010"}},
		{"v", "VARBIT(9)", "BIT", "(B'10101'),(B'11000'),(B'00111')", []any{"00000","11111","01010"}},
	} {
		for _, domain := range []bool{false, true} {
			tag, input := tc.tag, tc.input
			if domain {
				tag += "_domain"
				input = "bw_domain_"+tc.tag
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN "+input+" AS "+tc.input, 0))
			}
			t.Run(tag, func(t *testing.T) {
				table := "bw_metadata_"+tag
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(v "+input+" NOT NULL)", 0))
				q := "SELECT bit_and(v),bit_or(v),bit_xor(v) FROM "+table
				bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, q, tc.typ, []any{nil,nil,nil}))
				// Grouping BIT itself is an independent excluded provider. Use an
				// integer constant key to exercise empty keyed schema instead.
				empty := bitwiseAggregateTriple(b.PG, q+" GROUP BY (1+1)", tc.typ, []any{nil,nil,nil})
				empty.Rows = nil
				bitwiseAggregateRoutes(t, p, b, empty)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES"+tc.values, 3))
				bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, q, tc.typ, tc.row))
				bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, "SELECT bit_and(v) FILTER(WHERE FALSE),bit_or(v) FILTER(WHERE FALSE),bit_xor(v) FILTER(WHERE FALSE) FROM "+table, tc.typ, []any{nil,nil,nil}))
			})
		}
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE bw_source_metadata(f BIT(5),v VARBIT(9))", 0))
	s := fixture.Select("SELECT f,v FROM bw_source_metadata", []string{"f","v"}, []string{"BIT","VARBIT"}, nil)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
		for i, width := range []int64{5,9} {
			if n, ok := cols[i].Length(); !ok || n != width { t.Fatalf("source width %d/%v want %d/true", n, ok, width) }
		}
	}
	bitwiseAggregateRoutes(t, p, b, s)
}

func TestReferenceBitwiseAggregateMetadata(t *testing.T) {
	bitwiseAggregateScenario(t, bitwiseAggregateMetadataCases)
}
