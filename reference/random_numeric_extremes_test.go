package reference_test

import (
	"context"
	"database/sql"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

type randomExtremeCase struct {
	name, lo, hi string
	scale        int
	lattice      []string
}

func TestReferenceRandomNondegenerateNumericExtremes(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE random_extremes(id INT,lo NUMERIC,hi NUMERIC)", 0))
	fineZero := "0." + strings.Repeat("0", 16383)
	fineOne := "0." + strings.Repeat("0", 16382) + "1"
	fineTwo := "0." + strings.Repeat("0", 16382) + "2"
	maxWeight := "1" + strings.Repeat("0", 131071)
	cases := []randomExtremeCase{
		{"wide-multiword", "-18446744073709551616.000", "18446744073709551616.000", 3, nil},
		{"maximum-scale", fineZero, fineTwo, 16383, []string{fineZero, fineOne, fineTwo}},
		{"maximum-weight", maxWeight, "2" + maxWeight[1:], 0, nil},
	}
	// Bind text through real NUMERIC columns, independently expecting one
	// published row on each backend without recording enormous expected logs.
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			for i, tc := range cases {
				result, e := c.ExecContext(p.ctx, "INSERT INTO random_extremes VALUES($1,$2,$3)", i+1, tc.lo, tc.hi)
				if e != nil {
					message := e.Error()
					t.Fatal(tc.name, "input", message[:min(len(message), 200)])
				}
				if n, e := result.RowsAffected(); e != nil || n != 1 {
					t.Fatal(tc.name, "insert count", n, e)
				}
			}
			seed := func() { randomReseed(t, p, c, backend, .25) }
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.ctx, c, backend, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
					if err := rows.Scan(&value); err != nil {
						t.Fatal("extreme continuation float64 scan", err)
					}
					if rowIndex != 0 {
						t.Fatal("extreme continuation extra row", rowIndex)
					}
				})
				if len(values) != 1 || values[0][0] != value {
					t.Fatal("extreme continuation typed/raw mismatch", values, value)
				}
				if math.IsNaN(value) || value < 0 || value >= 1 {
					t.Fatal("extreme continuation range", value)
				}
				return value
			}
			for i, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					lower, ok := new(big.Rat).SetString(tc.lo)
					if !ok {
						t.Fatal("invalid independent lower expectation")
					}
					upper, ok := new(big.Rat).SetString(tc.hi)
					if !ok {
						t.Fatal("invalid independent upper expectation")
					}
					q := "SELECT random(lo,hi) FROM random_extremes WHERE id=$1"
					stmt, e := c.PrepareContext(p.ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					sample := func(prepared bool) string {
						return randomExtremeSample(t, p, c, backend, stmt, q, i, tc, lower, upper, prepared)
					}
					var direct []string
					var directNext float64
					for _, prepared := range []bool{false, true} {
						seed()
						a := []string{sample(prepared), sample(prepared)}
						next := draw()
						seed()
						b := []string{sample(prepared), sample(prepared)}
						again := draw()
						if !reflect.DeepEqual(a, b) || next != again {
							t.Fatal("same-backend reseeded samples/continuation changed")
						}
						if prepared {
							if !reflect.DeepEqual(a, direct) || next != directNext {
								t.Fatal("prepare/execution altered own-backend stream")
							}
						} else {
							direct = a
							directNext = next
						}
					}
				})
			}
		})
	}
}

func randomExtremeSample(t *testing.T, p *referencePair, c *sql.Conn, backend referenceBackend, stmt *sql.Stmt, q string, i int, tc randomExtremeCase, lower, upper *big.Rat, prepared bool) string {
	t.Helper()
	var rows *sql.Rows
	var e error
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
	if prepared {
		rows, e = stmt.QueryContext(ctx, i+1)
	} else {
		rows, e = c.QueryContext(ctx, q, i+1)
	}
	if e != nil {
		t.Fatal("numeric sample", e)
	}
	defer rows.Close()
	names, e := rows.Columns()
	if e != nil || !reflect.DeepEqual(names, []string{"random"}) {
		t.Fatal("sample labels", names, e)
	}
	types, e := rows.ColumnTypes()
	if e != nil || len(types) != 1 || types[0].DatabaseTypeName() != "NUMERIC" {
		t.Fatal("sample metadata", types, e)
	}
	precision, metadataScale, known := types[0].DecimalSize()
	// The pinned pq adapter decodes server typmod -1 as the
	// unsigned (typmod-4) fields in both direct/prepared rows.
	// Assert that observed representation separately; never
	// copy its fictitious sizing into native logical metadata.
	wantPrecision, wantScale, wantKnown := int64(0), int64(0), false
	if backend.pg {
		wantPrecision, wantScale, wantKnown = 65535, 65531, true
	}
	if precision != wantPrecision || metadataScale != wantScale || known != wantKnown {
		t.Fatal("adapter numeric sizing", precision, metadataScale, known, "want", wantPrecision, wantScale, wantKnown)
	}
	if !rows.Next() {
		t.Fatal("missing sample", rows.Err())
	}
	var raw any
	if e = rows.Scan(&raw); e != nil {
		t.Fatal("sample output", e)
	}
	referenceAssertRawValue(t, backend.pg, "NUMERIC", raw)
	var typedText string
	if err := rows.Scan(&typedText); err != nil {
		t.Fatal("numeric sample string scan", err)
	}
	text := referenceNormalize(raw).(string)
	if typedText != text {
		t.Fatal("numeric sample typed/raw mismatch; lengths", len(typedText), len(text))
	}
	text = typedText
	if rows.Next() || rows.Err() != nil {
		t.Fatal("sample row count", rows.Err())
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || value.Cmp(lower) < 0 || value.Cmp(upper) > 0 {
		t.Fatal("exact inclusive range violated; output length", len(text))
	}
	dot := strings.IndexByte(text, '.')
	scale := 0
	if dot >= 0 {
		scale = len(text) - dot - 1
	}
	if scale != tc.scale {
		t.Fatal("display scale", scale, "want", tc.scale)
	}
	// Canonical finite output contains digits and at most the
	// ordinary sign/decimal point, never exponential truncation.
	if strings.Trim(text, "-0123456789.") != "" || len(text) > max(len(tc.lo), len(tc.hi)) {
		t.Fatal("numeric output integrity; length", len(text))
	}
	// This entire nondegenerate interval has the maximum
	// integer digit count, so no lucky endpoint is required.
	if tc.name == "maximum-weight" {
		if len(text) != 131072 || text[0] == '0' || strings.Trim(text, "0123456789") != "" {
			t.Fatal("maximum-weight canonical integer output; length", len(text))
		}
	}
	if tc.lattice != nil {
		member := false
		for _, want := range tc.lattice {
			member = member || text == want
		}
		if !member {
			t.Fatal("minimum-unit lattice membership; output length", len(text))
		}
	}
	if e := rows.Close(); e != nil {
		t.Fatal(e)
	}
	randomRecorded(t, backend, q, []string{"random"}, []string{"NUMERIC"}, [][]any{{text}})
	return text
}
