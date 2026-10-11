package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericFormattingFeatureSmoke(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT to_char(9007199254740993::bigint,'FM9999999999999999') AS exact,to_char('-485'::numeric,'999PR') AS sign,to_char(485::numeric,'FMRN') AS roman,to_char(0.0004859::numeric,'9.99EEEE') AS science,to_number('123456','99999V99') AS scaled"
		s := fixture.Select(q, []string{"exact", "sign", "roman", "science", "scaled"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "NUMERIC"}, [][]any{{"9007199254740993", "<485>", "CDLXXXV", " 4.86e-04", "1234.560000000000000000"}})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { numericFormattingMetadata(t, r, b.PG) }
		s.CheckRaw = func(t *testing.T, i, j int, v any) bool { fixture.AssertRawValue(t, b.PG, s.Types[i], v); return true }
		t.Run("exact-sign-roman-science-scale", func(t *testing.T) { numericFormattingBoth(t, p, b, c, s) })
		t.Run("strict-null", func(t *testing.T) {
			numericFormattingBoth(t, p, b, c, fixture.Select("SELECT to_char(NULL::numeric,'9.9.9') AS rendered,to_number(NULL::text,'9EEEE') AS parsed", []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{nil, nil}}))
		})
		t.Run("C-and-smallint-contract", func(t *testing.T) {
			q := "SELECT to_char(1485,'9G999') AS grouped,to_char(148.5::numeric,'999D999') AS decimal,to_char(485,'L999') AS currency,to_char(1::smallint,'FM9.000000000000000000') AS small,to_char(1::float8,'FM9.000000000000000000') AS preferred,to_char(1::integer,'FM9.000000000000000000') AS integer"
			numericFormattingSelect(t, p, b, c, q, []string{"grouped", "decimal", "currency", "small", "preferred", "integer"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{" 1,485", " 148.500", "  485", "1.00000000000000", "1.00000000000000", "1.000000000000000000"}})
		})
		t.Run("held-bad-valid", func(t *testing.T) {
			q := "SELECT to_number($1::text,$2::text) AS parsed"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			bad := fixture.ErrorDiagnostic(q, numericFormattingDiagnostic("42601", "multiple decimal points", "", "", ""), "12", "9.9.9")
			bad.SelectRows, bad.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"parsed"}, []string{"NUMERIC"}, [][]any{{"12.50"}}, "12.50", "99.99"))
		})
		t.Run("closed-UUID-numeric-defaults-and-replay", func(t *testing.T) { numericFormattingUUIDDefaults(t, p, b, c) })
		t.Run("late-row-atomicity", func(t *testing.T) { numericFormattingAtomic(t, p, b, c) })
		t.Run("stored-default-view-and-native-replay", func(t *testing.T) { numericFormattingControls(t, p, b, c) })
	})
}
