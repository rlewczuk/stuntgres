package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceDatetimeFormattingSignaturesOwnerBoundary(t *testing.T) {
	cases := []datetimeFormattingCase{
		{name: "catalog timestamp", expression: "pg_catalog.to_char(TIMESTAMP '2006-10-19','YYYY')", want: "2006"},
		{name: "date implicit timestamp", expression: "to_char(DATE '2006-10-19','YYYY')", want: "2006"},
		{name: "varchar", expression: "to_date('05 Dec 2000'::varchar,'DD Mon YYYY'::varchar)::text", want: "2000-12-05"},
		{name: "bpchar", expression: "to_date('05 Dec 2000'::character(11),'DD Mon YYYY'::character(11))::text", want: "2000-12-05"},
		{name: "name", expression: "to_date('2000'::name,'YYYY'::name)::text"},
		{name: "quoted builtin", expression: "\"to_date\"('2000','YYYY')::text", want: "2000-01-01"},
	}
	for _, expr := range []string{
		"to_char(NULL,'YYYY')", "to_char('2000-01-01','YYYY')", "to_char(NULL,NULL)", "to_date(NULL,NULL)", "to_timestamp(NULL,NULL)",
		"to_char()", "to_char(TIMESTAMP '2000-01-01')", "to_char(TIMESTAMP '2000-01-01','YYYY','extra')", "to_date('2000')", "to_timestamp('2000','YYYY','extra')",
		"to_date(42,'YYYY')", "to_date('2000',42)", "to_char(true,'YYYY')",
		"missing_schema.to_date('2000','YYYY')", "public.to_date('2000','YYYY')", "\"TO_DATE\"('2000','YYYY')",
	} {
		cases = append(cases, datetimeFormattingCase{name: expr, expression: expr})
	}
	// These exact original routes keep their independent PostgreSQL contracts.
	// Native lacks the adjacent TIME-to-INTERVAL cast.
	timeBoundary := datetimeFormattingDiagnostic("0A000", "time to interval conversion is unsupported", "", "")
	timeBoundary.Position = "8"
	cases = append(cases, datetimeFormattingCase{name: "to_char(TIME '12:00','HH24')", expression: "to_char(TIME '12:00','HH24')", nativeDiagnostic: timeBoundary})
	cases = append(cases, datetimeFormattingCase{name: "to_date(VARIADIC ARRAY['2000','YYYY'])", expression: "to_date(VARIADIC ARRAY['2000','YYYY'])"})
	// Numeric signatures now route to the numeric formatting provider.
	for _, typ := range []string{"smallint", "integer", "bigint", "numeric", "real", "double precision"} {
		cases = append(cases, datetimeFormattingCase{name: "numeric owner/" + typ, expression: "to_char(12::" + typ + ",'FM999')", want: "12"})
	}
	d := datetimeFormattingDiagnostic("0A000", "epoch to_timestamp is unsupported", "", "")
	d.Position = "8"
	cases = append(cases, datetimeFormattingCase{name: "epoch owner", expression: "to_timestamp(0)::text", want: "1970-01-01 00:00:00+00", nativeDiagnostic: d})
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingDiagnosticPhases(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		queries := []string{
			"SELECT to_date('2006 2007','YYYY YYYY')",
			"SELECT to_date('2006 2006','YYYY IYYY')",
			"SELECT to_timestamp('13','HH12')",
			"SELECT to_date('2006-02-29','YYYY-MM-DD')",
			"SELECT to_date('20','YYYYMMDD')",
			"SELECT to_date('2x06','YYYY')",
			"SELECT to_date('notmonth','MONTH')",
			"SELECT to_timestamp('bogus','TZ')",
			"SELECT to_date('2147483648','YYYY')",
			"SELECT to_date('292','DDD')",
			"SELECT to_char(INTERVAL '1 day','MONTH')",
			"SELECT '雪', to_date('2000')",
			"SELECT to_date(" + strings.Repeat("'2000',", 100) + "'YYYY')",
			"SELECT to_char($1,$2)",
			"SELECT to_date($1,$2)",
			"SELECT to_date($1,$1), $1::integer",
			"SELECT to_date($1,'YYYY'), $1::integer",
			"SELECT to_date($2,'YYYY')",
		}
		for i, q := range queries {
			t.Run(fmt.Sprintf("phase/%d", i), func(t *testing.T) {
				args := []any(nil)
				if strings.Contains(q, "$1") {
					args = []any{"2000", "YYYY"}
					if !strings.Contains(q, "$2") {
						args = args[:1]
					}
				}
				if strings.Contains(q, "$2") && !strings.Contains(q, "$1") {
					args = []any{"unused", "2000"}
				}
				for _, prepared := range []bool{false, true} {
					datetimeFormattingAssertContract(t, p, b, conn, q, prepared, args...)
					expected, ok := datetimeFormattingFindContract(q)
					if !ok {
						t.Fatal("missing phase contract", q)
					}
					s := datetimeFormattingContractStep(expected, b.pg, q, prepared, args...)
					s.selectRows = false
					// Successful phase queries each independently return one row;
					// PostgreSQL's SELECT command tag reports that affected count.
					s.affected = 1
					referenceRunStep(t, p.ctx, conn, b.recorder, s)
				}
			})
		}
		// A parameterized held statement must prepare quietly and fail at
		// Execute, then remain usable with independently valid input.
		q := "SELECT to_date($1::text,$2::text)::text AS result"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
		d := datetimeFormattingDiagnostic("22008", `date/time field value out of range: "2006-02-29"`, "", "")
		s := referenceErrorDiagnostic(q, *d, "2006-02-29", "YYYY-MM-DD")
		s.selectRows = true
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"2000-02-29"}}, "2000-02-29", "YYYY-MM-DD"))
	})
}

func TestReferenceDatetimeFormattingDomainsNamespace(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN dt_format_text AS text", 0))
		run(referenceExec("CREATE DOMAIN dt_format_timestamp AS timestamp", 0))
		for _, prepared := range []bool{false, true} {
			for _, s := range []referenceStep{
				referenceSelect("SELECT to_date('2000'::dt_format_text,'YYYY'::dt_format_text)::text AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2000-01-01"}}),
				referenceSelect("SELECT to_char('2006-10-19'::dt_format_timestamp,'YYYY') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2006"}}),
			} {
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceExec("SET search_path=public", 0))
		run(referenceSelect("SELECT to_date('2000','YYYY')::text AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2000-01-01"}}))
	})
}
