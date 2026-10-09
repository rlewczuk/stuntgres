package reference_test

import (
	"database/sql"
	"math/big"
	"strings"
	"testing"
)

func TestReferenceRandomNumericEqualExtremes(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE extremes(id INT,lo NUMERIC,hi NUMERIC)", 0))
	p.step(t, referenceExec("CREATE TABLE rounded(lo NUMERIC(2,-3),hi NUMERIC(2,-3))", 0))
	p.step(t, referenceExec("INSERT INTO rounded VALUES(1500,1500)", 1))
	p.step(t, referenceSelect("SELECT random(lo,hi) FROM rounded", []string{"random"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
	for i, value := range []string{"0." + strings.Repeat("0", 16382) + "1", "1" + strings.Repeat("0", 131071)} {
		p.step(t, referenceExec("INSERT INTO extremes VALUES($1,$2,$3)", 1, i+1, value, value))
		step := referenceSelect("SELECT random(lo,hi) FROM extremes WHERE id=$1", []string{"random"}, []string{"NUMERIC"}, [][]any{{value}}, i+1)
		step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
			var text string
			if err := rows.Scan(&text); err != nil {
				t.Fatal("endpoint string scan", err)
			}
			if rowIndex != 0 || text != value {
				t.Fatal("endpoint string changed", rowIndex, len(text), len(value))
			}
		}
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
func TestReferenceRandomNumericLattices(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			for _, tc := range []struct {
				lo, hi string
				values []string
			}{
				{"100000000000000000000.000", "100000000000000000000.002", []string{"100000000000000000000.000", "100000000000000000000.001", "100000000000000000000.002"}},
				{"0.00000000000000000000", "0.00000000000000000002", []string{"0.00000000000000000000", "0.00000000000000000001", "0.00000000000000000002"}},
			} {
				for range 16 {
					v := randomScalar(t, p, c, b, "SELECT random("+tc.lo+","+tc.hi+")", "random", "NUMERIC")
					found := false
					for _, want := range tc.values {
						found = found || v == want
					}
					if !found {
						t.Fatal("lattice", v)
					}
				}
			}
			for range 16 {
				text := randomScalar(t, p, c, b, "SELECT random(-0.499,0.499)", "random", "NUMERIC").(string)
				value, ok := new(big.Rat).SetString(text)
				dot := strings.IndexByte(text, '.')
				if !ok || dot < 0 || len(text)-dot-1 != 3 || value.Cmp(big.NewRat(-499, 1000)) < 0 || value.Cmp(big.NewRat(499, 1000)) > 0 {
					t.Fatal("negative lattice", text)
				}
			}
		})
	}
}
