package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestReferenceFormatEncodingFloatPrecision(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		// Columns are shortest, precision 0, -1 and -15. Independent edge
		// expectations were validated by the pinned PostgreSQL18.0 discovery.
		vectors := []struct {
			source, typ string
			text        [4]string
		}{
			{"1.23456789", "float4", [4]string{"1.2345679", "1.23457", "1.2346", "1"}},
			{"1.2345678901234567", "float8", [4]string{"1.2345678901234567", "1.23456789012346", "1.2345678901235", "1"}},
			{"33554448", "float4", [4]string{"3.3554448e+07", "3.35544e+07", "3.3554e+07", "3e+07"}},
			{"1.8014398509481992e+16", "float8", [4]string{"1.8014398509481992e+16", "1.8014398509482e+16", "1.8014398509482e+16", "2e+16"}},
			{"1e-45", "float4", [4]string{"1e-45", "1.4013e-45", "1.4013e-45", "1e-45"}},
			{"5e-324", "float8", [4]string{"5e-324", "4.94065645841247e-324", "4.9406564584125e-324", "5e-324"}},
			{"3.4028235e+38", "float4", [4]string{"3.4028235e+38", "3.40282e+38", "3.4028e+38", "3e+38"}},
			{"1.7976931348623157e+308", "float8", [4]string{"1.7976931348623157e+308", "1.79769313486232e+308", "1.7976931348623e+308", "2e+308"}},
			{"2.2250738585072014e-308", "float8", [4]string{"2.2250738585072014e-308", "2.2250738585072e-308", "2.2250738585072e-308", "2e-308"}},
			{"100000", "float4", [4]string{"100000", "100000", "1e+05", "1e+05"}},
			{"0.0001", "float4", [4]string{"0.0001", "0.0001", "0.0001", "0.0001"}},
			{"0.00001", "float8", [4]string{"1e-05", "1e-05", "1e-05", "1e-05"}},
			{"9.999999", "float4", [4]string{"9.999999", "10", "10", "1e+01"}},
			{"-0", "float4", [4]string{"-0", "-0", "-0", "-0"}},
			{"-0", "float8", [4]string{"-0", "-0", "-0", "-0"}},
			{"NaN", "float4", [4]string{"NaN", "NaN", "NaN", "NaN"}},
			{"Infinity", "float8", [4]string{"Infinity", "Infinity", "Infinity", "Infinity"}},
			{"-Infinity", "float8", [4]string{"-Infinity", "-Infinity", "-Infinity", "-Infinity"}},
		}
		for _, precision := range []int{1, 2, 3, 0, -1, -15} {
			run(referenceExec(fmt.Sprintf("SET extra_float_digits TO %d", precision), 0))
			index := 0
			if precision == 0 {
				index = 1
			}
			if precision == -1 {
				index = 2
			}
			if precision == -15 {
				index = 3
			}
			for _, tc := range vectors {
				t.Run(fmt.Sprintf("%s/%s/precision=%d", tc.typ, tc.source, precision), func(t *testing.T) {
					q := "SELECT '" + tc.source + "'::" + tc.typ + "::text AS v"
					step := formatEncodingText(q, "v", tc.text[index])
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					formatEncodingHeld(t, p, b, c, step, nil)
				})
			}
		}
		run(referenceExec("SET extra_float_digits TO -6", 0))
		run(formatEncodingText("SELECT '1.23456789'::float4::text AS v", "v", "1"))
		run(referenceExec("SET extra_float_digits TO -15", 0))
		run(formatEncodingText("SELECT '1.2345678901234567'::float8::text AS v", "v", "1"))
		for _, value := range []int{-16, 4} {
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("%d is outside the valid range for parameter \"extra_float_digits\" (-15 .. 3)", value)}
			run(referenceImplicitError(fmt.Sprintf("SET extra_float_digits TO %d", value), want))
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
		}
		run(referenceImplicitError("SET extra_float_digits TO 'bogus'", formatEncodingInvalid("extra_float_digits", "bogus", "")))
		// Scalar export remains exact native binary; pq decodes server-rounded text.
		run(referenceExec("CREATE TABLE fe_binary(v double precision)", 0))
		run(referenceExec("INSERT INTO fe_binary VALUES('1.2345678901234567')", 1))
		run(referenceExec("SET extra_float_digits TO 0", 0))
		want := 1.2345678901234567
		if b.pg {
			want = 1.23456789012346
		}
		step := referenceSelect("SELECT v FROM fe_binary", []string{"v"}, []string{"FLOAT8"}, [][]any{{want}})
		step.checkRaw = func(t *testing.T, column, row int, value any) bool {
			v, ok := value.(float64)
			if !ok || math.Float64bits(v) != math.Float64bits(want) {
				t.Fatalf("scalar transport=%T(%v), want bits %#x", value, value, math.Float64bits(want))
			}
			return true
		}
		run(step)
		run(referenceExec("SET extra_float_digits TO 3", 0))
		run(formatEncodingText("SELECT v::text AS v FROM fe_binary", "v", "1.2345678901234567"))
	})
}

func TestReferenceFormatEncodingConsumers(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN fe_float AS double precision", 0))
		run(referenceExec("CREATE TABLE fe_consumer(id integer, v double precision, text_value text DEFAULT ('1.2345678901234567'::float8)::text, CHECK (id>0))", 0))
		run(referenceExec("CREATE VIEW fe_view AS SELECT v::text AS v FROM fe_consumer", 0))
		for _, tc := range []struct{ precision, r, d string }{{"1", "1.2345679", "1.2345678901234567"}, {"0", "1.23457", "1.23456789012346"}, {"-15", "1", "1"}} {
			run(referenceExec("SET extra_float_digits TO "+tc.precision, 0))
			for _, q := range []string{"SELECT '1.2345678901234567'::fe_float::text AS v", "SELECT format('%s','1.2345678901234567'::float8) AS v", "SELECT ''::text || '1.2345678901234567'::float8 AS v", "SELECT '1.2345678901234567'::float8::varchar::text AS v"} {
				t.Run(tc.precision+"/"+q, func(t *testing.T) {
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText(q, "v", tc.d))
					formatEncodingHeld(t, p, b, c, formatEncodingText(q, "v", tc.d), nil)
				})
			}
			// Selected character IO also preserves target padding/truncation.
			// These new paths require fresh pinned validation with Consumers.
			for _, character := range []struct{ source, typ, text string }{
				{"1.23456789", "float4", tc.r},
				{"1.2345678901234567", "float8", tc.d},
			} {
				short := character.text
				if len(short) > 8 {
					short = short[:8]
				}
				for _, edge := range []struct{ cast, typ, text string }{
					{"varchar(8)", "VARCHAR", short},
					{"char(20)", "BPCHAR", character.text + strings.Repeat(" ", 20-len(character.text))},
					{"char(20)::text", "TEXT", character.text},
				} {
					q := "SELECT '" + character.source + "'::" + character.typ + "::" + edge.cast + " AS v"
					step := referenceSelect(q, []string{"v"}, []string{edge.typ}, [][]any{{edge.text}})
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					formatEncodingHeld(t, p, b, c, step, nil)
				}
			}
			// Existing array input exercises presentation without admitting
			// the unrelated general ARRAY constructor syntax.
			realArray := formatEncodingText("SELECT '{1.23456789}'::float4[]::text AS v", "v", "{"+tc.r+"}")
			run(realArray)
			formatEncodingHeld(t, p, b, c, realArray, nil)
			array := referenceSelect("SELECT '{1.2345678901234567}'::float8[] AS v", []string{"v"}, []string{"_FLOAT8"}, [][]any{{"{" + tc.d + "}"}})
			run(array)
			formatEncodingHeld(t, p, b, c, array, nil)
			run(referenceExec("INSERT INTO fe_consumer(id,v) VALUES(1,'1.2345678901234567')", 1))
			run(formatEncodingText("SELECT text_value AS v FROM fe_consumer", "v", tc.d))
			run(referencePrepared(formatEncodingText("SELECT v FROM fe_view", "v", tc.d)))
			run(referenceExec("DELETE FROM fe_consumer", 1))
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "fe_consumer" violates check constraint "fe_consumer_id_check"`, Detail: "Failing row contains (-1, " + tc.d + ", " + tc.d + ").", Schema: "public", Table: "fe_consumer", Constraint: "fe_consumer_id_check"}
			run(referenceImplicitError("INSERT INTO fe_consumer(id,v) VALUES(-1,'1.2345678901234567')", want))
		}
		const numericQuery = "SELECT to_char($1::numeric,$2) AS v"
		const timeQuery = "SELECT to_char($1::timestamp,$2) AS v"
		numericStmt, err := c.PrepareContext(p.ctx, numericQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer numericStmt.Close()
		timeStmt, err := c.PrepareContext(p.ctx, timeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer timeStmt.Close()
		for _, locale := range []string{"C", "POSIX", ""} {
			for _, name := range []string{"lc_numeric", "lc_monetary", "lc_time"} {
				run(referenceExec("SET "+name+" TO '"+locale+"'", 0))
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{statement: numericStmt, query: numericQuery}, b.recorder, formatEncodingText(numericQuery, "v", "-1,234.50", "-1234.5", "FMS9G999D00"))
			referenceRunStep(t, p.ctx, referenceHeldStatement{statement: timeStmt, query: timeQuery}, b.recorder, formatEncodingText(timeQuery, "v", "October Thursday", "2006-10-19", "TMMonth TMDay"))
			for _, tc := range []struct{ q, want string }{
				{"SELECT to_char(-1234.5::numeric,'FMS9G999D00') AS v", "-1,234.50"},
				{"SELECT to_char(12::numeric,'FML99') AS v", " 12"},
				{"SELECT to_char(to_number('1,234.50','9G999D99'),'FM9999.00') AS v", "1234.50"},
				{"SELECT to_char(TIMESTAMP '2006-10-19','TMMonth TMDay') AS v", "October Thursday"},
				{"SELECT to_char(TIMESTAMP '2006-10-19','Month') AS v", "October  "},
				{"SELECT to_date('05 dEc 2000','DD TMMon YYYY')::text AS v", "2000-12-05"},
				{"SELECT to_char(12.5::numeric,'FM99.00') AS v", "12.50"},
			} {
				t.Run(locale+"/"+tc.q, func(t *testing.T) {
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText(tc.q, "v", tc.want))
					formatEncodingHeld(t, p, b, c, formatEncodingText(tc.q, "v", tc.want), nil)
				})
			}
			formatEncodingHeld(t, p, b, c, formatEncodingText("SELECT to_char($1::numeric,$2) AS v", "v", "-1,234.50", "-1234.5", "FMS9G999D00"), nil)
			formatEncodingHeld(t, p, b, c, formatEncodingText("SELECT to_char($1::timestamp,$2) AS v", "v", "October Thursday", "2006-10-19", "TMMonth TMDay"), nil)
			formatEncodingHeld(t, p, b, c, formatEncodingText("SELECT to_char(to_number($1,$2),'FM9999.00') AS v", "v", "1234.50", "1,234.50", "9G999D99"), nil)
			formatEncodingHeld(t, p, b, c, formatEncodingText("SELECT to_date($1,$2)::text AS v", "v", "2000-12-05", "05 dEc 2000", "DD TMMon YYYY"), nil)
		}
		run(referenceExec("CREATE TABLE fe_masks(v text DEFAULT to_char(12::numeric,'FML99'), CHECK (v=to_char(12::numeric,'FML99')))", 0))
		run(referenceExec("INSERT INTO fe_masks DEFAULT VALUES", 1))
		run(formatEncodingText("SELECT v FROM fe_masks", "v", " 12"))
		run(referenceExec("CREATE VIEW fe_masks_view AS SELECT to_char(TIMESTAMP '2006-10-19','TMMonth TMDay') AS v", 0))
		run(referencePrepared(formatEncodingText("SELECT v FROM fe_masks_view", "v", "October Thursday")))
		run(referenceSelect("SELECT '1.2345678901234567'::numeric AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.2345678901234567"}}))
	})
}
