package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceFormatDatatypeOutput(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN format_padded AS char(3)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN format_number AS numeric(5,2)", 0))
		for _, tc := range []struct{ call, want string }{
			{"format('%s/%I/%L','x'::format_padded,'x'::format_padded,'x'::format_padded)", `x  /"x  "/'x  '`},
			{"format('%s/%L',5::format_number,5::format_number)", "5.00/'5.00'"},
			{"format('%s/%s/%s',42::smallint,42::bigint,42::oid)", "42/42/42"},
			{"format('%s','123e4567-e89b-12d3-a456-426614174000'::uuid)", "123e4567-e89b-12d3-a456-426614174000"},
			{"format('%s/%L','x  '::varchar(4),'x  '::varchar(4))", "x  /'x  '"},
			{"format('%s '::char(5),'x')", "x"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_internal_char(v int)", 0))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format('%s/%I/%L',relkind,relkind,relkind) AS v FROM pg_catalog.pg_class WHERE relname='format_internal_char'", []string{"v"}, []string{"TEXT"}, [][]any{{"r/r/'r'"}}))
		q := `SELECT format('%s|%s|%s|%s',E'\\x41425c'::bytea,'2000-01-02'::date,'2000-01-02 03:04:05+00'::timestamptz,'1 day 02:03:04'::interval) AS v`
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct{ name, bytea, date, zone, interval, want string }{
			{"iso-hex", "hex", "ISO, MDY", "UTC", "postgres", `\x41425c|2000-01-02|2000-01-02 03:04:05+00|1 day 02:03:04`},
			{"iso-escape-zone", "escape", "ISO, MDY", "Europe/Helsinki", "postgres", `AB\\|2000-01-02|2000-01-02 05:04:05+02|1 day 02:03:04`},
			{"iso-interval", "hex", "ISO, MDY", "UTC", "iso_8601", `\x41425c|2000-01-02|2000-01-02 03:04:05+00|P1DT2H3M4S`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, set := range []string{"SET bytea_output='" + tc.bytea + "'", "SET DateStyle='" + tc.date + "'", "SET TimeZone='" + tc.zone + "'", "SET IntervalStyle='" + tc.interval + "'"} {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(set, 0))
				}
				s := referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, held, b.recorder, s)
			})
		}
		dateQuery := "SELECT format('%s','2000-01-02'::date) AS v"
		dateStatement, err := c.PrepareContext(p.ctx, dateQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer dateStatement.Close()
		dateHeld := referenceHeldStatement{referenceExecutor: c, statement: dateStatement, query: dateQuery}
		for _, tc := range []struct{ style, want string }{{"SQL, MDY", "01/02/2000"}, {"SQL, DMY", "02/01/2000"}, {"ISO, MDY", "2000-01-02"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET DateStyle='"+tc.style+"'", 0))
			referenceRunStep(t, p.ctx, dateHeld, b.recorder, referenceSelect(dateQuery, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
	})
}

func TestReferenceFormatRegclass(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_reg(v int)", 0))
		var oid int64
		if err := c.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_reg'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT format('%s/%I/%L',$1::regclass,$1::regclass,$1::regclass) AS v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct{ set, want string }{
			{"SET search_path=public,pg_catalog", "format_reg/format_reg/'format_reg'"},
			{"SET search_path=pg_catalog", `public.format_reg/"public.format_reg"/'public.format_reg'`},
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(tc.set, 0))
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, oid))
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("ALTER TABLE public.format_reg RENAME TO format_renamed", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{`public.format_renamed/"public.format_renamed"/'public.format_renamed'`}}, oid))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET search_path=public,pg_catalog", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"format_renamed/format_renamed/'format_renamed'"}}, oid))
	})
}
