package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateValues(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	bitwiseAggregateSetup(t, p, b)
	for _, tc := range []struct{ column, typ string }{{"s", "INT2"}, {"i", "INT4"}, {"b", "INT8"}, {"f", "BIT"}, {"v", "BIT"}} {
		t.Run(tc.column, func(t *testing.T) {
			fold, single := []any{int64(0), int64(15), int64(5)}, []any{int64(7), int64(7), int64(7)}
			if tc.typ == "BIT" {
				fold, single = []any{"00000", "11111", "01010"}, []any{"00101", "00101", "00101"}
			}
			q := "SELECT bit_and("+tc.column+"),bit_or("+tc.column+"),bit_xor("+tc.column+") FROM bw"
			for _, predicate := range []struct{ sql string; row []any }{
				{"k='fold'", fold}, {"k='single'", single}, {"k='null'", []any{nil,nil,nil}}, {"k='missing'", []any{nil,nil,nil}}, {"FALSE", []any{nil,nil,nil}},
			} {
				bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, q+" WHERE "+predicate.sql, tc.typ, predicate.row))
			}
			bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, "SELECT bit_and("+tc.column+") FILTER(WHERE FALSE),bit_or("+tc.column+") FILTER(WHERE NULL),bit_xor("+tc.column+") FILTER(WHERE FALSE) FROM bw", tc.typ, []any{nil,nil,nil}))
		})
	}
	for _, q := range []string{
		"SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE k='fold'",
		"SELECT pg_catalog.bit_and(i),\"bit_or\"(i),bit_xor(i) FROM bw WHERE k='fold'",
		"SELECT bit_and(ALL i),bit_or(ALL i),bit_xor(ALL i) FROM bw WHERE k='fold'",
	} {
		bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, q, "INT4", []any{int64(0),int64(15),int64(5)}))
	}
	bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, "SELECT bit_and(7),bit_or(7),bit_xor(7)", "INT4", []any{int64(7),int64(7),int64(7)}))
	bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, "SELECT bit_and(i ORDER BY b DESC),bit_or(i) FILTER(WHERE k='single'),bit_xor(i) FILTER(WHERE FALSE) FROM bw", "INT4", []any{int64(0),int64(7),nil}))
	bitwiseAggregateRoutes(t, p, b, bitwiseAggregateTriple(b.PG, "SELECT bit_and(f ORDER BY i),bit_or(f),bit_xor(f) FROM bw WHERE k='fold'", "BIT", []any{"00000","11111","01010"}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE bw_dup(i INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO bw_dup VALUES(6),(3),(6),(NULL)", 4))
	bitwiseAggregateRoutes(t, p, b, fixture.Select("SELECT bit_and(i),bit_or(i),bit_xor(i),bit_xor(DISTINCT i) FROM bw_dup", []string{"bit_and","bit_or","bit_xor","bit_xor"}, []string{"INT4","INT4","INT4","INT4"}, [][]any{{int64(2),int64(7),int64(3),int64(5)}}))
	bitwiseAggregateRoutes(t, p, b, fixture.Select("SELECT bit_and(i),bit_or(i),bit_xor(i),bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE k='fold'", []string{"bit_and","bit_or","bit_xor","bit_and","bit_or","bit_xor"}, []string{"INT4","INT4","INT4","INT4","INT4","INT4"}, [][]any{{int64(0),int64(15),int64(5),int64(0),int64(15),int64(5)}}))
	grouped := fixture.Select("SELECT k,bit_and(i),bit_or(i),bit_xor(i) FROM bw GROUP BY k", []string{"k","bit_and","bit_or","bit_xor"}, []string{"TEXT","INT4","INT4","INT4"}, [][]any{{"fold",int64(0),int64(15),int64(5)},{"single",int64(7),int64(7),int64(7)},{"null",nil,nil,nil}})
	grouped.Bag = true
	bitwiseAggregateRoutes(t, p, b, grouped)
	grouped.Query, grouped.Rows = "SELECT k,bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE FALSE GROUP BY k", nil
	bitwiseAggregateRoutes(t, p, b, grouped)
	bitwiseAggregateRoutes(t, p, b, fixture.Select("SELECT k FROM bw GROUP BY k HAVING bit_and(i)=0 AND bit_or(i)=15 AND bit_xor(i)=5", []string{"k"}, []string{"TEXT"}, [][]any{{"fold"}}))
}

func TestReferenceBitwiseAggregateValues(t *testing.T) {
	bitwiseAggregateScenario(t, bitwiseAggregateValues)
}
