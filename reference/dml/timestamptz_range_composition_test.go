package reference_test

import (
	"bytes"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

// This second package's Composition definition reuses the existing real COPY
// transport fixture. The selector intentionally exercises both packages.
func TestReferenceTimestamptzRangeBinaryComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := p.Native
			if b.PG {
				db = p.CopyPostgres
			}
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			c := copyTextConnection{conn: conn, pg: b.PG, ctx: p.Ctx}
			c.exec(t, "CREATE TABLE copy_timestamptz_range(k integer NOT NULL,v tstzrange)")
			// Independently specified from the range binary format: the independently authored range flag byte,
			// network endpoint lengths and signed 8-byte timestamp microseconds below.
			// No expected bytes come from backend output or a production codec.
			endpoint := func(n int64) []byte { return append(copyBinary32(8), copyBinary64(n)...) }
			bounded := func(flags byte, lo, hi int64) []byte {
				value := []byte{flags}
				value = append(value, endpoint(lo)...)
				return append(value, endpoint(hi)...)
			}
			fields := [][]byte{
				bounded(0, 0, 1),                         // exclusive 0 to 1us, nonempty continuous interval
				bounded(6, math.MinInt64, math.MaxInt64), // inclusive explicit infinities
				{24},                                     // both bounds omitted, not explicit infinity
				{1},                                      // empty
				nil,                                      // SQL NULL
				append([]byte{16}, endpoint(math.MaxInt64)...), // (infinity,)
				append([]byte{8}, endpoint(math.MinInt64)...),  // (,-infinity)
			}
			rows := make([][]byte, len(fields))
			for i, v := range fields {
				rows[i] = copyBinaryRow(copyBinary32(int32(i+1)), v)
			}
			wire := copyBinaryStream(rows...)
			result, err := c.from("COPY copy_timestamptz_range FROM STDIN WITH(FORMAT binary)", bytes.NewReader(wire))
			copyTextSuccess(t, result, err, int64(len(fields)))
			c.selectRows(t, "SELECT k,v FROM copy_timestamptz_range ORDER BY k", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{
				{int64(1), `("2000-01-01 00:00:00+00","2000-01-01 00:00:00.000001+00")`},
				{int64(2), "[-infinity,infinity]"}, {int64(3), "(,)"}, {int64(4), "empty"}, {int64(5), nil}, {int64(6), "(infinity,)"}, {int64(7), "(,-infinity)"},
			})
			c.output(t, "COPY (SELECT k,v FROM copy_timestamptz_range ORDER BY k) TO STDOUT WITH(FORMAT binary)", string(wire), int64(len(fields)))
			c.exec(t, "SET TIME ZONE 'America/New_York'")
			c.selectRows(t, "SELECT v FROM copy_timestamptz_range WHERE k=1", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{`("1999-12-31 19:00:00-05","1999-12-31 19:00:00.000001-05")`}})
			c.output(t, "COPY (SELECT k,v FROM copy_timestamptz_range ORDER BY k) TO STDOUT WITH(FORMAT binary)", string(wire), int64(len(fields)))
		})
	}
}
