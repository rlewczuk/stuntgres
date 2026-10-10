package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Manual Table 9.31 and REL_18_0 numeric.out vectors; exact decimal/integer
// arithmetic supplies the additional ordinary ties and precision witnesses.
func TestReferenceNumericFormattingOutput(t *testing.T) {
	numericFormattingCases(t, []numericFormattingCase{
		{"optional", "to_char('-0.1'::numeric,'99.99')", "", "  -.10"},
		{"fill", "to_char('-0.1'::numeric,'FM9.99')", "", "-.1"},
		{"mandatory", "to_char('-0.1'::numeric,'FM90.99')", "", "-0.1"},
		{"zero-position", "to_char(12,'9990999.9')", "", "    0012.0"},
		{"zero-position-fill", "to_char(12,'FM9990999.9')", "", "0012."},
		{"mandatory-fraction", "to_char(148.5::numeric,'FM999.990')", "", "148.500"},
		{"empty-fraction", "to_char(100::numeric,'FM999.9')", "", "100."},
		{"no-fraction", "to_char(100::numeric,'FM999.')", "", "100"},
		{"tie-positive", "to_char(2.5::numeric,'FM9')", "", "3"},
		{"tie-negative", "to_char('-2.5'::numeric,'FM9')", "", "-3"},
		{"carry", "to_char(99.95::numeric,'FM999.0')", "", "100.0"},
		{"zero", "to_char(0::numeric,'FM000')", "", "000"},
		{"int4-min", "to_char('-2147483648'::integer,'FM9999999999')", "", "-2147483648"},
		{"int4-max", "to_char(2147483647::integer,'FM9999999999')", "", "2147483647"},
		{"int8-min", "to_char('-9223372036854775808'::bigint,'FM9999999999999999999')", "", "-9223372036854775808"},
		{"int8-max", "to_char(9223372036854775807::bigint,'FM9999999999999999999')", "", "9223372036854775807"},
		{"exact-beyond-float", "to_char(9007199254740993::bigint,'FM9999999999999999')", "", "9007199254740993"},
		{"exact-decimal", "to_char(12345678901234567890.125::numeric,'FM99999999999999999999.000')", "", "12345678901234567890.125"},
		{"float4", "to_char(12.5::real,'FM99.0')", "", "12.5"},
		{"float8", "to_char(12.5::double precision,'FM99.0')", "", "12.5"},
		{"float-cap-overflow", "to_char('12345678901'::float8,'FM9999999999D9999900000000000000000')", "", "##########.####"},
		{"minus-position", "to_char(-12,'MI9999')", "", "-  12"},
		{"sign-anchor", "to_char(-12,'S9999')", "", "  -12"},
		{"brackets", "to_char(-485,'999PR')", "", "<485>"},
		{"positive-mi", "to_char(485,'999MI')", "", "485 "},
		{"positive-mi-fill", "to_char(485,'FM999MI')", "", "485"},
		{"roman-pad", "to_char(485,'RN')", "", "        CDLXXXV"},
		{"roman-fill", "to_char(485,'FMRN')", "", "CDLXXXV"},
		{"roman-fraction", "to_char(5.2::numeric,'FMRN')", "", "V"},
		{"roman-lower", "to_char(485,'FMrn')", "", "cdlxxxv"},
		{"ordinal", "to_char(482,'999th')", "", " 482nd"},
		{"ordinal-1", "to_char(1,'FM9TH')", "", "1ST"},
		{"ordinal-2", "to_char(2,'FM9th')", "", "2nd"},
		{"ordinal-3", "to_char(3,'FM9th')", "", "3rd"},
		{"ordinal-4", "to_char(4,'FM9th')", "", "4th"},
		{"ordinal-11", "to_char(11,'FM99th')", "", "11th"},
		{"ordinal-12", "to_char(12,'FM99th')", "", "12th"},
		{"ordinal-13", "to_char(13,'FM99th')", "", "13th"},
		{"ordinal-21", "to_char(21,'FM99th')", "", "21st"},
		{"scale", "to_char(12.45::numeric,'99V9')", "", " 125"},
		{"science", "to_char(0.0004859::numeric,'9.99EEEE')", "", " 4.86e-04"},
		{"science-extreme", "to_char('1.2345e131071'::numeric,'9.999EEEE')", "", " 1.235e+131071"},
		{"empty-mask", "to_char(12::numeric,'')", "", ""},
		{"strict-source", "to_char(NULL::numeric,'9.9.9')", "", nil},
		{"strict-mask", "to_char(12::numeric,NULL::text)", "", nil},
		{"quoted-token", `to_char(12,'FM99"RN"')`, "", "12RN"},
		{"utf8-literal", `to_char(12,'FM99"é猫"')`, "", "12é猫"},
		{"qualified", "pg_catalog.to_char(12::integer,'FM99')", "", "12"},
		{"quoted-name", "\"to_char\"(12::integer,'FM99')", "", "12"},
	})
}

func TestReferenceNumericFormattingInput(t *testing.T) {
	cases := []numericFormattingCase{
		{"groups-fraction", "to_number('-34,338,492.654,878','99G999G999D999G999')", "NUMERIC", "-34338492.654878"},
		{"brackets", "to_number('<564646.654564>','999999.999999PR')", "NUMERIC", "-564646.654564"},
		{"suffix-sign", "to_number('5.01-','FM9.999999S')", "NUMERIC", "-5.01"},
		{"suffix-minus", "to_number('5.01-','FM9.999999MI')", "NUMERIC", "-5.01"},
		{"fraction-only", "to_number('.01','FM9.99')", "NUMERIC", "0.01"},
		{"actual-fraction-scale", "to_number('.0','99999999.99999999')", "NUMERIC", "0.0"},
		{"missing-fraction", "to_number('0','99.99')", "NUMERIC", "0"},
		{"plain-group", "to_number('34,50','999,99')", "NUMERIC", "3450"},
		{"trailing-group", "to_number('123,000','999G')", "NUMERIC", "123"},
		{"absent-group", "to_number('123456','999G999')", "NUMERIC", "123456"},
		{"currency-skip", "to_number('$1,234.56','L99,999.99')", "NUMERIC", "1234.56"},
		{"ordinal-skip", "to_number('42nd','99th')", "NUMERIC", "42"},
		{"scale-retained", "to_number('123456','99999V99')", "NUMERIC", "1234.560000000000000000"},
		{"roman-case", "to_number('CvIiI','rn')", "NUMERIC", "108"},
		{"roman-prefix", "to_number('M CC','RN')", "NUMERIC", "1000"},
		{"empty-mask", "to_number('123','')", "NUMERIC", nil},
		{"strict-source", "to_number(NULL::text,'9EEEE')", "NUMERIC", nil},
		{"strict-mask", "to_number('1',NULL::text)", "NUMERIC", nil},
		{"leading-zero", "to_number('0012.50','0000.00')", "NUMERIC", "12.50"},
		{"exact-large", "to_number('12345678901234567890.125','99999999999999999999.999')", "NUMERIC", "12345678901234567890.125"},
	}
	numericFormattingCases(t, cases)
}

func TestReferenceNumericFormattingScientificSpecials(t *testing.T) {
	var cases []numericFormattingCase
	for _, typ := range []string{"numeric", "real", "double precision"} {
		for _, special := range []string{"Infinity", "-Infinity", "NaN"} {
			cases = append(cases, numericFormattingCase{typ + special, "to_char(" + numericFormattingQuote(special) + "::" + typ + ",'9.999EEEE')", "", " #.#######"})
		}
	}
	numericFormattingCases(t, cases)
}

func TestReferenceNumericFormattingRomanCanonical(t *testing.T) {
	// Independent canonical decomposition, not the implementation's encoder.
	roman := func(n int) string {
		var out strings.Builder
		for _, v := range []struct {
			n    int
			text string
		}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
			for n >= v.n {
				out.WriteString(v.text)
				n -= v.n
			}
		}
		return out.String()
	}
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// Public reference covers decomposition transitions and both bounds;
		// exhaustive 1..3999 belongs to the Stage C provider unit test.
		for _, n := range []int{1, 4, 9, 14, 39, 40, 49, 90, 99, 400, 499, 900, 999, 1000, 1984, 3999} {
			t.Run(fmt.Sprint(n), func(t *testing.T) {
				want := roman(n)
				q := "SELECT to_char($1::integer,'FMRN') AS roman,to_number($2::text,'RN') AS number"
				numericFormattingBoth(t, p, b, c, referenceSelect(q, []string{"roman", "number"}, []string{"TEXT", "NUMERIC"}, [][]any{{want, fmt.Sprint(n)}}, int64(n), want))
			})
		}
	})
}

func TestReferenceNumericFormattingEdgeValues(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		witnesses := []struct {
			name, expression string
			names            []string
			want             []any
		}{
			{"smallint-optional", "to_char(1::smallint,'FM9.999999999999999999') AS s,to_char(1::double precision,'FM9.999999999999999999') AS f,to_char(1::integer,'FM9.999999999999999999') AS i", []string{"s", "f", "i"}, []any{"1.", "1.", "1."}},
			{"smallint-mandatory", "to_char(1::smallint,'FM9.000000000000000000') AS s,to_char(1::double precision,'FM9.000000000000000000') AS f,to_char(1::integer,'FM9.000000000000000000') AS i", []string{"s", "f", "i"}, []any{"1.00000000000000", "1.00000000000000", "1.000000000000000000"}},
			{"C-locale", "to_char(1485,'9G999') AS g,to_char(148.5::numeric,'999D999') AS d,to_char(485,'L999') AS l", []string{"g", "d", "l"}, []any{" 1,485", " 148.500", "  485"}},
			{"overflow-carry", "to_char(9.995::numeric,'FM9.99') AS result", []string{"result"}, []any{"#.##"}},
			{"global-fm", "to_char(12::numeric,'99FM.99') AS late,to_char(12::numeric,'FMFM99.99') AS repeated,to_char(12::numeric,'fm99.99') AS lower,to_char(12::numeric,'Fm99.99') AS mixed", []string{"late", "repeated", "lower", "mixed"}, []any{"12.", "12.", "12.", "Fm 12.00"}},
			{"lexical", `to_char(12,'FM99"unfinished') AS unmatched,to_char(12,'FM99\"x') AS escaped,to_char(12,'FM99Mi') AS mixed,to_char(12,'FM99B') AS blank,to_char(12,'FM99C') AS c,to_char(12,'FM99SP') AS sp`, []string{"unmatched", "escaped", "mixed", "blank", "c", "sp"}, []any{"12unfinished", `12"x`, "12Mi", "12", "12", "12"}},
			{"roman-bounds", "to_char(0,'FMRN') AS zero,to_char(4000,'FMRN') AS high,to_char(-1,'FMRN') AS negative,to_char('NaN'::numeric,'FMRN') AS nan,to_char('Infinity'::float8,'FMRN') AS inf", []string{"zero", "high", "negative", "nan", "inf"}, []any{"###############", "###############", "###############", "###############", "###############"}},
			{"roman-rounding", "to_char(2.5::numeric,'FMRN') AS n,to_char(2.5::float8,'FMRN') AS f,to_char(2.5::real,'FMRN') AS r", []string{"n", "f", "r"}, []any{"III", "II", "II"}},
			{"ordinal-suppression", "to_char(-21,'FM99TH') AS negative,to_char(21.0::numeric,'FM99.0TH') AS decimal,to_char(123,'FM9TH') AS overflow", []string{"negative", "decimal", "overflow"}, []any{"-21", "21.0", "#"}},
			{"float-rounding", "to_char(2.5::real,'FM9') AS r,to_char(2.5::float8,'FM9') AS f,to_char('-0'::float8,'FM9.0') AS zero,to_char(1::real,'FM9.000000000000000000') AS cap", []string{"r", "f", "zero", "cap"}, []any{"2", "2", "-.0", "1.00000"}},
			{"science-carry", "to_char(9.995::numeric,'9.99EEEE') AS carry,to_char(0::numeric,'9.99EEEE') AS zero,to_char('-0.0004859'::numeric,'9.99EEEE') AS negative", []string{"carry", "zero", "negative"}, []any{" 10.00e+00", " 0.00e+00", "-4.86e-04"}},
			{"science-int8", "to_char('-9223372036854775808'::bigint,'9.999999999999999999EEEE') AS low,to_char(9223372036854775807::bigint,'9.999999999999999999EEEE') AS high", []string{"low", "high"}, []any{"-9.223372036854775808e+18", " 9.223372036854775807e+18"}},
			{"science-float-extremes", "to_char('4.9406564584124654e-324'::float8,'9.999EEEE') AS subnormal,to_char('1.7976931348623157e308'::float8,'9.999EEEE') AS maximum,to_char('1.401298464324817e-45'::real,'9.999EEEE') AS r", []string{"subnormal", "maximum", "r"}, []any{" 4.941e-324", " 1.798e+308", " 1.401e-45"}},
			{"science-min-numeric", "to_char('1e-16383'::numeric,'9.999EEEE') AS low,to_char('-1.2345e131071'::numeric,'9.999EEEE') AS high", []string{"low", "high"}, []any{" 1.000e-16383", "-1.235e+131071"}},
		}
		for _, w := range witnesses {
			t.Run(w.name, func(t *testing.T) {
				types := make([]string, len(w.names))
				for i := range types {
					types[i] = "TEXT"
				}
				numericFormattingSelect(t, p, b, c, "SELECT "+w.expression, w.names, types, [][]any{w.want})
			})
		}
		for _, w := range []struct {
			mask string
			want [3]string
		}{
			{"S999", [3]string{" -12", "  +0", " +12"}}, {"999S", [3]string{" 12-", "  0+", " 12+"}},
			{"MI999", [3]string{"- 12", "   0", "  12"}}, {"999MI", [3]string{" 12-", "  0 ", " 12 "}},
			{"PL999", [3]string{"  -12", "+   0", "+  12"}}, {"999PL", [3]string{" -12 ", "   0+", "  12+"}},
			{"SG999", [3]string{"- 12", "+  0", "+ 12"}}, {"999SG", [3]string{" 12-", "  0+", " 12+"}},
			{"999PR", [3]string{" <12>", "   0 ", "  12 "}}, {"FM999PR", [3]string{"<12>", "0", "12"}},
			{"L999", [3]string{"  -12", "    0", "   12"}}, {"99,99", [3]string{"   -12", "     0", "    12"}},
			{"9G9G9", [3]string{"  -1,2", "     0", "   1,2"}},
			{"RN9", [3]string{"###############", "###############", "            XII"}},
			{"9RN", [3]string{"###############", "###############", "            XII"}},
			{"RNG", [3]string{"############### ", "############### ", "            XII "}},
			{"9GEEEE", [3]string{"-1e+01", " 0e+00", " 1e+01"}},
			{`9EEEE"tail"`, [3]string{"-1e+01", " 0e+00", " 1e+01"}},
			{"9V0", [3]string{"-12", "  0", " 12"}}, {"9V9V9", [3]string{"-###", "   0", " ###"}},
			{"9V", [3]string{"-#", " 0", " #"}}, {"V9", [3]string{"-#", " 0", " #"}},
			{"9V00", [3]string{" -12", "  00", "  12"}},
		} {
			t.Run(w.mask, func(t *testing.T) {
				for i, n := range []string{"-12", "0", "12"} {
					numericFormattingSelect(t, p, b, c, "SELECT to_char("+numericFormattingQuote(n)+"::numeric,$1::text) AS result", []string{"result"}, []string{"TEXT"}, [][]any{{w.want[i]}}, w.mask)
				}
			})
		}
		for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
			for _, typ := range []string{"numeric", "real", "double precision"} {
				for _, mask := range []string{"FM9999999999", "999", "FM9.99"} {
					t.Run(special+typ+mask, func(t *testing.T) {
						want := special
						if mask == "999" {
							want = " ###"
							if special == "NaN" {
								want = " NaN"
							}
							if special == "-Infinity" {
								want = "-###"
							}
						} else if mask == "FM9.99" {
							want = "#.##"
							if typ == "real" && special != "NaN" {
								want = "#."
							}
							if special == "-Infinity" {
								want = "-" + want
							}
						}
						numericFormattingSelect(t, p, b, c, "SELECT to_char("+numericFormattingQuote(special)+"::"+typ+",$1::text) AS result", []string{"result"}, []string{"TEXT"}, [][]any{{want}}, mask)
					})
				}
			}
		}
	})
}

func TestReferenceNumericFormattingPermissiveParsing(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, w := range []struct{ source, mask, want, message string }{
			{"12", "9999", "12", ""}, {"123tail", "99", "12", ""}, {" 12", "999", "12", ""}, {"1x2", "9x9", "12", ""}, {"猫12", "\"é\"99", "12", ""}, {"€12", "L99", "12", ""},
			{"", "99", "", `invalid input syntax for type numeric: " "`}, {" ", "99", "", `invalid input syntax for type numeric: " "`}, {"abc", "99", "", `invalid input syntax for type numeric: " "`},
			{"--1", "99", "-1", ""}, {"+-1", "99", "1", ""}, {"1-", "99", "1", ""}, {"1+", "99", "1", ""}, {"1.2.3", "99.99", "1.23", ""},
			{"+12", "S99", "12", ""}, {"12+", "99S", "12", ""}, {"-12", "MI99", "-12", ""}, {"12-", "99MI", "-12", ""}, {"+12", "PL99", "12", ""}, {"12+", "99PL", "12", ""}, {"-12", "SG99", "-12", ""}, {"12-", "99SG", "-12", ""},
			{"12", "9V0", "12.0000000000000000", ""}, {"12", "9V", "1.0000000000000000", ""}, {"123", "9V9V9", "1.230000000000000000", ""}, {"12", "V9", "0.10000000000000000", ""}, {" XIIrest", "RN", "12", ""}, {"MMMMMMMMMMMMMMMMI", "RN", "", "invalid Roman numeral"},
		} {
			t.Run(w.source+"/"+w.mask, func(t *testing.T) {
				q := "SELECT to_number($1::text,$2::text) AS result"
				if w.message != "" {
					numericFormattingBoth(t, p, b, c, numericFormattingError(q, numericFormattingDiagnostic("22P02", w.message, "", "", ""), w.source, w.mask))
				} else {
					numericFormattingSelect(t, p, b, c, q, []string{"result"}, []string{"NUMERIC"}, [][]any{{w.want}}, w.source, w.mask)
				}
			})
		}
		for _, count := range []int{0, 1, 2, 16, 984, 985, 1000, 1001, 1002, 16366, 16367, 16383, 16384} {
			t.Run(fmt.Sprintf("V-count-%d", count), func(t *testing.T) {
				mask := "9V" + strings.Repeat("9", count)
				want := "1." + strings.Repeat("0", 16)
				if count > 0 && count <= 1000 {
					scale := count + 16
					if scale > 1000 {
						scale = 1000
					}
					want = "0." + strings.Repeat("0", count-1) + "1" + strings.Repeat("0", scale-count)
				}
				if count > 1000 {
					want = "0." + strings.Repeat("0", 1000)
				}
				s := referenceSelect("SELECT to_number($1::text,$2::text) AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{want}}, "1", mask)
				s.prepared = true
				s.checkMetadata = func(t *testing.T, r *sql.Rows) { numericFormattingMetadata(t, r, b.pg) }
				s.checkRaw = func(t *testing.T, i, j int, v any) bool { referenceAssertRawValue(t, b.pg, "NUMERIC", v); return true }
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		}
	})
}

// tests-5 pinned direct/held observations distinguish the integer wrappers
// from NUMERIC without routing formatting through adjacent SQL arithmetic.
func TestReferenceNumericFormattingIntegerWrappers(t *testing.T) {
	numericFormattingCases(t, []numericFormattingCase{
		{"int4-science-carry", "to_char(999::integer,'9EEEE')", "", " 1e+03"},
		{"int8-science-carry", "to_char(999::bigint,'9EEEE')", "", " 10e+02"},
		{"numeric-science-carry", "to_char(999::numeric,'9EEEE')", "", " 10e+02"},
		{"int4-V-product", "to_char(2147483647::integer,'FM9999999999V9')", "", "-10"},
		{"int4-V-factor9", "to_char(1::integer,'FM9V999999999')", "", "1000000000"},
		{"int4-V-factor10", "to_char(1::integer,'FM9V9999999999')", "", "-2147483648"},
		{"int8-V-factor18", "to_char(1::bigint,'FM9V999999999999999999')", "", "1000000000000000000"},
	})
	// Dynamic masks preserve the same direct and genuinely held contracts.
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, w := range []struct{ typ, mask, want string }{
			{"integer", "FM9V999999999", "1000000000"},
			{"integer", "FM9V9999999999", "-2147483648"},
			{"bigint", "FM9V999999999999999999", "1000000000000000000"},
		} {
			numericFormattingSelect(t, p, b, c, "SELECT to_char(1::"+w.typ+",$1::text) AS result", []string{"result"}, []string{"TEXT"}, [][]any{{w.want}}, w.mask)
		}
	})
}
