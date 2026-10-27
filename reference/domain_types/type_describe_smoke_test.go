package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeSmoke(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeGraph(run)
		proof := func(run func(fixture.Step)) {
			s := fixture.Select("SELECT pg_typeof(NULL::integer) AS identity,format_type(1700,327686) AS declaration,pg_basetype('td.outer_domain'::regtype) AS base,pg_typeof(NULL::td.outer_domain)::oid='td.outer_domain'::regtype::oid AS nominal,format_type('td.outer_domain'::regtype::oid,NULL) AS qualified,format_type(0,NULL) AS zero,format_type(4294967295::oid,NULL) AS missing,pg_basetype(0::oid::regtype) AS absent", []string{"identity", "declaration", "base", "nominal", "qualified", "zero", "missing", "absent"}, []string{"REGTYPE", "TEXT", "REGTYPE", "BOOL", "TEXT", "TEXT", "TEXT", "REGTYPE"}, [][]any{{"integer", "numeric(5,2)", "numeric", true, "td.outer_domain", "-", "???", nil}})
			typeDescribeBoth(run, s)
			m := fixture.Select("SELECT pg_typeof(NULL::integer) AS identity,format_type(23,NULL) AS label", []string{"identity", "label"}, []string{"REGTYPE", "TEXT"}, [][]any{{"integer", "integer"}})
			m.CheckMetadata = func(t *testing.T, rows *sql.Rows) { typeDescribeMetadata(t, rows, b.PG, m.Columns, m.Types) }
			typeDescribeBoth(run, m)
		}
		proof(run)
		run(fixture.Exec("CREATE SEQUENCE public.td_smoke_seq START 1", 0))
		run(fixture.Select("SELECT pg_typeof(nextval('public.td_smoke_seq')) AS identity", []string{"identity"}, []string{"REGTYPE"}, [][]any{{"bigint"}}))
		run(fixture.Select("SELECT currval('public.td_smoke_seq') AS calls", []string{"calls"}, []string{"INT8"}, [][]any{{int64(1)}}))
		if b.PG {
			return
		}
		old, err := b.DB.QueryContext(p.Ctx, "SELECT pg_typeof(NULL::td.outer_domain) AS identity")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		proof(run)
		fixture.RegtypeAssertRows(t, old, [][]any{{"td.outer_domain"}}, false)
	})
}
