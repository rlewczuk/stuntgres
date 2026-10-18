package reference_test

import (
	"database/sql"
	"encoding/csv"
	"os"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func regoperatorInventory(t *testing.T) [][]string {
	t.Helper()
	f, e := os.Open("../../docs/design/regoperator-pg18-inventory.tsv")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.LazyQuotes = true
	r.FieldsPerRecord = 8
	rows, e := r.ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 800 {
		t.Fatalf("release inventory rows %d want header + 799", len(rows))
	}
	return rows[1:]
}
func TestReferenceRegOperatorInventory(t *testing.T) {
	inventory := regoperatorInventory(t)
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			expected := make([][]any, 0, len(inventory))
			for _, r := range inventory {
				expected = append(expected, []any{r[0], r[1], r[2], r[3], r[4]})
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid,oprnamespace,oprname::text,oprleft,oprright FROM pg_catalog.pg_operator WHERE oprnamespace=11 ORDER BY oid", []string{"oid", "oprnamespace", "oprname", "oprleft", "oprright"}, []string{"OID", "OID", "TEXT", "OID", "OID"}, expected))
			// OID transport remains pq decimal bytes; the general fixture normalizes them to strings.
		}
		for _, r := range inventory {
			t.Run(r[0], func(t *testing.T) {
				oid, _ := strconv.ParseInt(r[0], 10, 64)
				for _, tc := range []struct{ alias, input, text string }{{"regoper", r[0], r[6]}, {"regoperator", r[0], r[7]}, {"regoperator", r[5], r[7]}} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorRows("SELECT $1::"+tc.alias+" AS v,$1::"+tc.alias+"::oid AS o", tc.alias, tc.text, oid, b.PG, tc.input))
				}
			})
		}
		// Nested typmods identify numeric, not a second comma-separated argument.
		for _, r := range inventory {
			if r[2] == "+" && r[3] == "1700" && r[4] == "1700" {
				oid, _ := strconv.ParseInt(r[0], 10, 64)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorRows("SELECT $1::regoperator AS v,$1::regoperator::oid AS o", "regoperator", r[7], oid, b.PG, "+(numeric(10,2),numeric(8,3))"))
			}
		}
	})
}
