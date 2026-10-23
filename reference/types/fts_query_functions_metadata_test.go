package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsMetadata(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, d := range []struct {
			oid, result                                      int
			name, signature, volatility, parallel, call, typ string
			want                                             any
		}{
			{3672, 23, "numnode", "tsquery", "i", "s", "numnode('a&b'::tsquery)", "INT4", int64(3)},
			{3673, 25, "querytree", "tsquery", "i", "s", "querytree('a&!b'::tsquery)", "TEXT", "'a'"},
			{3684, 3615, "ts_rewrite", "tsquery,tsquery,tsquery", "i", "s", "ts_rewrite('a'::tsquery,'a'::tsquery,'b'::tsquery)", "TSQUERY", "'b'"},
			{3685, 3615, "ts_rewrite", "tsquery,text", "v", "u", "ts_rewrite('a'::tsquery,'VALUES (''a''::tsquery,''b''::tsquery)')", "TSQUERY", "'b'"},
		} {
			t.Run(fmt.Sprint(d.oid), func(t *testing.T) {
				signature := "pg_catalog." + d.name + "(" + d.signature + ")"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT oid::bigint AS oid,proname,oid="+ftsTypesQuote(signature)+"::regprocedure::oid AS signature,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_catalog.pg_proc WHERE oid="+fmt.Sprint(d.oid), []string{"oid", "proname", "signature", "result", "proisstrict", "provolatile", "proparallel"}, []string{"INT8", "NAME", "BOOL", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(d.oid), d.name, true, int64(d.result), true, d.volatility, d.parallel}}))
				for _, call := range []string{d.call, "pg_catalog." + d.call} {
					ftsQueryFunctionsValue(t, p, b, call, d.typ, d.want)
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT "+call, []string{d.name}, []string{d.typ}, [][]any{{d.want}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
				}
			})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE fts_qf_meta(id integer,q tsquery)", 0))
		query := "SELECT numnode(q),querytree(q),ts_rewrite(q,'a'::tsquery,'b'::tsquery),ts_rewrite(q,'SELECT t,s FROM (VALUES (''a''::tsquery,''b''::tsquery)) AS rules(t,s)') AS sql_rewrite FROM fts_qf_meta ORDER BY id"
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(query, []string{"numnode", "querytree", "ts_rewrite", "sql_rewrite"}, []string{"INT4", "TEXT", "TSQUERY", "TSQUERY"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO fts_qf_meta VALUES(1,'a'),(2,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(query, []string{"numnode", "querytree", "ts_rewrite", "sql_rewrite"}, []string{"INT4", "TEXT", "TSQUERY", "TSQUERY"}, [][]any{{int64(1), "'a'", "'b'", "'b'"}, {nil, nil, nil, nil}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}

func TestReferenceFTSQueryFunctionsAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fts_qf_reader"})
	fixture.SchemaReferenceCleanup(t, "fts_qf_space")
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE fts_qf_reader", 0))
		run(fixture.Exec("CREATE SCHEMA fts_qf_space", 0))
		run(fixture.Exec("CREATE TABLE fts_qf_space.rules(t tsquery,s tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_qf_space.rules VALUES('a','z')", 1))
		run(fixture.Exec("GRANT USAGE ON SCHEMA fts_qf_space TO fts_qf_reader", 0))
		run(fixture.Exec("GRANT SELECT ON fts_qf_space.rules TO fts_qf_reader", 0))
		run(fixture.Exec("SET search_path TO fts_qf_space,pg_catalog", 0))
		defer conn.ExecContext(p.Ctx, "RESET search_path")
		query := "SELECT ts_rewrite('a'::tsquery,'SELECT t,s FROM rules') AS rewritten"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		run(fixture.Exec("SET ROLE fts_qf_reader", 0))
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'z'"}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE SELECT ON fts_qf_space.rules FROM fts_qf_reader", 0))
		run(fixture.Exec("SET ROLE fts_qf_reader", 0))
		for range 2 {
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table rules")
			d.Context = "SQL statement \"SELECT t,s FROM rules\""
			s := fixture.ErrorDiagnostic(query, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'z'"}}))
		for _, signature := range []string{"numnode(tsquery)", "querytree(tsquery)", "ts_rewrite(tsquery,tsquery,tsquery)", "ts_rewrite(tsquery,text)"} {
			t.Run(signature, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+signature+" FROM PUBLIC", 0))
				defer conn.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+signature+" TO PUBLIC")
				run(fixture.Exec("SET ROLE fts_qf_reader", 0))
				defer conn.ExecContext(p.Ctx, "RESET ROLE")
				call := "numnode('a'::tsquery)"
				switch signature {
				case "querytree(tsquery)":
					call = "querytree('a'::tsquery)"
				case "ts_rewrite(tsquery,tsquery,tsquery)":
					call = "ts_rewrite('a'::tsquery,'a'::tsquery,'b'::tsquery)"
				case "ts_rewrite(tsquery,text)":
					call = "ts_rewrite('a'::tsquery,'VALUES (''a''::tsquery,''b''::tsquery)')"
				}
				name := "ts_rewrite"
				if signature == "numnode(tsquery)" {
					name = "numnode"
				}
				if signature == "querytree(tsquery)" {
					name = "querytree"
				}
				s := fixture.ErrorDiagnostic("SELECT "+call, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name))
				s.SelectRows = true
				run(s)
			})
		}
	})
}

func TestReferenceFTSQueryFunctionsHeldInputs(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		query := "SELECT $1::tsquery AS original,numnode($1::tsquery) AS nodes,querytree($1::tsquery) AS tree,ts_rewrite($1::tsquery,$2::tsquery,$3::tsquery) AS direct,ts_rewrite($1::tsquery,$4::text) AS rules"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		for range 2 {
			for _, tc := range []struct {
				input any
				row   []any
			}{
				{"a", []any{"'a'", int64(1), "'a'", "'z'", "'b'"}},
				{"!a", []any{"!'a'", int64(2), "T", "!'z'", "!'b'"}},
				{nil, []any{nil, nil, nil, nil, nil}},
			} {
				s := fixture.Select(query, []string{"original", "nodes", "tree", "direct", "rules"}, []string{"TSQUERY", "INT4", "TEXT", "TSQUERY", "TSQUERY"}, [][]any{tc.row}, tc.input, "a", "z", "VALUES ('a'::tsquery,'b'::tsquery)")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
	})
}
