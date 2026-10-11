package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func moneyReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func moneyReferenceRun(t *testing.T, p *fixture.Pair, b fixture.Backend, steps ...fixture.Step) {
	t.Helper()
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func moneyReferenceValue(expression, typ string, value any) fixture.Step {
	return fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{value}})
}

func moneyReferenceFailure(query, code, message, position, context, stage string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message, Position: position, Context: context})
	s.SelectRows = strings.HasPrefix(query, "SELECT")
	s.ErrorStage = stage
	return s
}

func moneyReferenceLiteralPosition(query, literal string) string {
	return fixture.Position(utf8.RuneCountInString(query[:strings.Index(query, literal)]) + 1)
}

func TestReferenceMoneyIOAndBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			moneyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { s.Prepared = prepared; moneyReferenceRun(t, p, b, s) }
				run(fixture.Exec(`CREATE TABLE money_io(id INT,v MONEY)`, 0))
				for i, tc := range []struct{ input, output string }{
					{"123", "$123.00"}, {"$123.454", "$123.45"}, {"$123.455", "$123.46"}, {"-123.455", "-$123.46"},
					{"($123,456.78)", "-$123,456.78"}, {"-0", "$0.00"}, {"92233720368547758.07", "$92,233,720,368,547,758.07"},
					{"-92233720368547758.08", "-$92,233,720,368,547,758.08"}, {"92233720368547758.074", "$92,233,720,368,547,758.07"},
					{"-92233720368547758.084", "-$92,233,720,368,547,758.08"}, {"", "$0.00"}, {"  ", "$0.00"}, {"$", "$0.00"},
					{"1,2,3.4", "$123.40"}, {"(1", "-$1.00"}, {"1)", "$1.00"}, {"1-", "-$1.00"}, {"1+", "$1.00"}, {"1$$", "$1.00"},
				} {
					run(fixture.Exec("INSERT INTO money_io VALUES($1,$2)", 1, int64(i), tc.input))
					run(fixture.Select("SELECT v FROM money_io WHERE id=$1", []string{"v"}, []string{"MONEY"}, [][]any{{tc.output}}, int64(i)))
				}
				for _, expression := range []string{`MONEY '1.235'`, `'1.235'::money`, `CAST('1.235' AS money)`, `'1.235'::pg_catalog.money`, `'1.235'::"money"`} {
					run(moneyReferenceValue(expression, "MONEY", "$1.24"))
				}
				for _, input := range []string{"oops", "1e2", "NaN", "Infinity", "0x10", "1_0", "1.2x"} {
					q := "SELECT '" + input + "'::money"
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					run(moneyReferenceFailure(q, "22P02", "invalid input syntax for type money: \""+input+"\"", "8", "", stage))
				}
				for _, input := range []string{"92233720368547758.08", "-92233720368547758.09", "92233720368547758.075", "-92233720368547758.085"} {
					q := "SELECT '" + input + "'::money"
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					run(moneyReferenceFailure(q, "22003", "value \""+input+"\" is out of range for type money", "8", "", stage))
				}
			})
		})
	}
}

func TestReferenceMoneyLocaleAndStability(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		moneyReferenceRun(t, p, b, fixture.Exec("CREATE TABLE money_locale(v MONEY)", 0), fixture.Exec("INSERT INTO money_locale VALUES('1234.5')", 1))
		for _, locale := range []string{"C", "POSIX", ""} {
			moneyReferenceRun(t, p, b, fixture.Exec("SET lc_monetary TO '"+locale+"'", 0), fixture.Select("SHOW lc_monetary", []string{"lc_monetary"}, []string{"TEXT"}, [][]any{{locale}}), fixture.Select("SELECT v FROM money_locale", []string{"v"}, []string{"MONEY"}, [][]any{{"$1,234.50"}}), moneyReferenceValue("to_char(1234.5,'LFM9999.00')", "TEXT", " 1234.50"))
		}
		moneyReferenceRun(t, p, b, fixture.Exec("SET lc_monetary TO 'C'", 0), fixture.Exec("BEGIN", 0), fixture.Exec("SET LOCAL lc_monetary TO 'POSIX'", 0), fixture.Select("SHOW lc_monetary", []string{"lc_monetary"}, []string{"TEXT"}, [][]any{{"POSIX"}}), fixture.Exec("ROLLBACK", 0), fixture.Select("SHOW lc_monetary", []string{"lc_monetary"}, []string{"TEXT"}, [][]any{{"C"}}))
		for _, q := range []string{"CREATE INDEX money_numeric_idx ON money_locale ((v::numeric))", "CREATE INDEX money_text_idx ON money_locale ((v::text))"} {
			moneyReferenceRun(t, p, b, moneyReferenceFailure(q, "42P17", "functions in index expression must be marked IMMUTABLE", "", "", "execute"))
		}
		moneyReferenceRun(t, p, b, moneyReferenceFailure("CREATE TABLE money_generated(v MONEY,n NUMERIC GENERATED ALWAYS AS (v::numeric) STORED)", "42P17", "generation expression is not immutable", "", "", "execute"), fixture.Exec("CREATE INDEX money_raw_idx ON money_locale ((v+'1'::money))", 0))
		for _, prepared := range []bool{false, true} {
			steps := []fixture.Step{
				fixture.Select("SELECT v FROM money_locale WHERE v+'1'::money='1235.5'::money", []string{"v"}, []string{"MONEY"}, [][]any{{"$1,234.50"}}),
				fixture.Exec("INSERT INTO money_locale VALUES('2')", 1),
				fixture.Select("SELECT v FROM money_locale WHERE v+'1'::money='3'::money", []string{"v"}, []string{"MONEY"}, [][]any{{"$2.00"}}),
				fixture.Exec("UPDATE money_locale SET v=v+'0.01'::money WHERE v='2'::money", 1),
				fixture.Select("SELECT v FROM money_locale WHERE v+'1'::money='3'::money", []string{"v"}, []string{"MONEY"}, nil),
				fixture.Select("SELECT v FROM money_locale WHERE v+'1'::money='3.01'::money", []string{"v"}, []string{"MONEY"}, [][]any{{"$2.01"}}),
				fixture.Exec("DELETE FROM money_locale WHERE v+'1'::money='3.01'::money", 1),
				fixture.Select("SELECT v FROM money_locale WHERE v+'1'::money='3.01'::money", []string{"v"}, []string{"MONEY"}, nil),
			}
			for _, s := range steps {
				s.Prepared = prepared
				moneyReferenceRun(t, p, b, s)
			}
		}
		if !b.PG {
			s := fixture.ImplicitError("SET lc_monetary TO 'en_US.UTF-8'", fixture.Diagnostic{Code: "22023", Message: "invalid value for parameter \"lc_monetary\": \"en_US.UTF-8\"", Detail: "Only C and POSIX locales are supported by stuntgres."})
			moneyReferenceRun(t, p, b, s, fixture.Select("SHOW lc_monetary", []string{"lc_monetary"}, []string{"TEXT"}, [][]any{{"C"}}), fixture.Select("SELECT v FROM money_locale", []string{"v"}, []string{"MONEY"}, [][]any{{"$1,234.50"}}))
		}
	})
}

func moneyReferenceMetadata(t *testing.T, rows *sql.Rows, postgres, declared bool, nullable []bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(nullable) {
		t.Fatal(columns, nullable)
	}
	for i, c := range columns {
		length, hasLength := c.Length()
		precision, scale, sized := c.DecimalSize()
		null, known := c.Nullable()
		if c.DatabaseTypeName() != "MONEY" || length != 0 || hasLength || precision != 0 || scale != 0 || sized || known != (!postgres && declared) || null != (!postgres && declared && nullable[i]) {
			t.Fatal(c, length, hasLength, precision, scale, sized, null, known)
		}
		moneyReferenceScanType(t, c, postgres)
	}
}
