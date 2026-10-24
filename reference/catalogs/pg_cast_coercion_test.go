package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgCastCoercion(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE pgcast_seed(i INT,b BIGINT,v VARCHAR(5),j JSON,jb JSONB,flag BOOL)", 0))
	run(fixture.Exec("INSERT INTO pgcast_seed VALUES(7,9,'abcd','{\"n\":1}','{\"n\":1}',true)", 1))
	fixture.PgCastCheckFacts(t, p, b, b.DB, [][]any{
		{int64(23), int64(20), int64(481), "i", "f"},
		{int64(20), int64(23), int64(480), "a", "f"},
		{int64(23), int64(16), int64(2557), "e", "f"},
		{int64(16), int64(23), int64(2558), "e", "f"},
		{int64(16), int64(25), int64(2971), "a", "f"},
		{int64(114), int64(3802), int64(0), "a", "i"},
		{int64(3802), int64(114), int64(0), "a", "i"},
		{int64(1043), int64(1043), int64(669), "i", "f"},
	}, false)
	s := fixture.Select("SELECT CASE WHEN flag THEN i ELSE b END AS mixed,i::int8 AS wide,b::int4 AS narrow,i::bool AS truth,flag::int4 AS one,(-1)::int4::oid AS high,'4294967295'::oid::int4 AS signed,flag::text AS words,false::bool::text AS no,j::jsonb AS binary,jb::json AS textual,v::varchar(2) AS short,NULL::bool::text AS absent FROM pgcast_seed",
		[]string{"mixed", "wide", "narrow", "truth", "one", "high", "signed", "words", "no", "binary", "textual", "short", "absent"},
		[]string{"INT8", "INT8", "INT4", "BOOL", "INT4", "OID", "INT4", "TEXT", "TEXT", "JSONB", "JSON", "VARCHAR", "TEXT"},
		[][]any{{int64(7), int64(7), int64(9), true, int64(1), int64(4294967295), int64(-1), "true", "false", "{\"n\": 1}", "{\"n\": 1}", "ab", nil}})
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	run(fixture.Exec("CREATE TABLE pgcast_assignment(wide BIGINT,narrow INT,words TEXT,j JSON,jb JSONB)", 0))
	for _, prepared := range []bool{false, true} {
		s = fixture.Exec("INSERT INTO pgcast_assignment SELECT i,b,flag,jb,j FROM pgcast_seed", 1)
		s.Prepared = prepared
		run(s)
	}
	run(fixture.Exec("INSERT INTO pgcast_assignment VALUES(NULL,NULL,NULL,NULL,NULL)", 1))
	s = fixture.Select("SELECT * FROM pgcast_assignment ORDER BY wide NULLS LAST", []string{"wide", "narrow", "words", "j", "jb"}, []string{"INT8", "INT4", "TEXT", "JSON", "JSONB"},
		[][]any{{int64(7), int64(9), "true", "{\"n\": 1}", "{\"n\": 1}"}, {int64(7), int64(9), "true", "{\"n\": 1}", "{\"n\": 1}"}, {nil, nil, nil, nil, nil}})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("CREATE TABLE pgcast_short(v VARCHAR(2))", 0))
	run(fixture.Exec("INSERT INTO pgcast_short VALUES('ok')", 1))
	for _, prepared := range []bool{false, true} {
		s = fixture.ImplicitError("INSERT INTO pgcast_short SELECT v FROM pgcast_seed", fixture.Diagnostic{Code: "22001", Message: "value too long for type character varying(2)"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		// A later input row fails after an earlier valid row was evaluated.
		s = fixture.ImplicitError("INSERT INTO pgcast_short VALUES('a'::varchar(5)),('abcd'::varchar(5))", fixture.Diagnostic{Code: "22001", Message: "value too long for type character varying(2)"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(fixture.Select("SELECT * FROM pgcast_short", []string{"v"}, []string{"VARCHAR"}, [][]any{{"ok"}}))
	}
	run(fixture.Exec("CREATE TABLE pgcast_flags(flag BOOL)", 0))
	run(fixture.Exec("CREATE TABLE pgcast_ints(i INT)", 0))
	run(fixture.Exec("INSERT INTO pgcast_flags VALUES(false)", 1))
	run(fixture.Exec("INSERT INTO pgcast_ints VALUES(42)", 1))
	for _, tc := range []struct{ query, expr, column, target, source string }{
		{"INSERT INTO pgcast_flags SELECT i FROM pgcast_seed", "i FROM", "flag", "boolean", "integer"},
		{"INSERT INTO pgcast_ints SELECT flag FROM pgcast_seed", "flag FROM", "i", "integer", "boolean"},
	} {
		for _, prepared := range []bool{false, true} {
			want := fixture.Diagnostic{Code: "42804", Message: "column \"" + tc.column + "\" is of type " + tc.target + " but expression is of type " + tc.source,
				Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(tc.query, tc.expr) + 1)}
			s = fixture.ImplicitError(tc.query, want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			run(fixture.Select("SELECT * FROM pgcast_flags", []string{"flag"}, []string{"BOOL"}, [][]any{{false}}))
			run(fixture.Select("SELECT * FROM pgcast_ints", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
	}
	const heldQuery = "SELECT $1::int4::int8 AS wide,$2::bool::text AS words,$3::json::jsonb AS binary,$4::varchar(5)::varchar(2) AS short FROM pgcast_seed"
	stmt, err := b.DB.PrepareContext(p.Ctx, heldQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: heldQuery}
	for _, tc := range []struct {
		args []any
		row  []any
	}{
		{[]any{7, true, "{\"n\":1}", "abcd"}, []any{int64(7), "true", "{\"n\": 1}", "ab"}},
		{[]any{nil, nil, nil, nil}, []any{nil, nil, nil, nil}},
		{[]any{9, false, "{\"n\":2}", "xy"}, []any{int64(9), "false", "{\"n\": 2}", "xy"}},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldQuery, []string{"wide", "words", "binary", "short"}, []string{"INT8", "TEXT", "JSONB", "VARCHAR"}, [][]any{tc.row}, tc.args...))
	}
	s = pgCastParameterError(heldQuery, "integer", "bad", "bad", true, "{}", "ok")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldQuery, []string{"wide", "words", "binary", "short"}, []string{"INT8", "TEXT", "JSONB", "VARCHAR"}, [][]any{{int64(11), "true", "{}", "ok"}}, 11, true, "{}", "ok"))
}

func TestReferencePGCastCoercionClosure(t *testing.T) { pgCastScenario(t, pgCastCoercion) }

func TestReferencePGCastExclusions(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		absent := [][]any{{int64(23), int64(25)}, {int64(25), int64(23)}, {int64(701), int64(25)}, {int64(26), int64(25)}, {int64(25), int64(25)}, {int64(23), int64(23)}, {int64(16), int64(16)}}
		query := "SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast" + fixture.PgCastPredicate(absent)
		s := fixture.Select(query, fixture.PgCastFactColumns, fixture.PgCastFactTypes, [][]any{})
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		fact := []any{int64(3802), int64(23), int64(3451), "e", "f"}
		want := [][]any{fact}
		s = fixture.Select("SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast"+fixture.PgCastPredicate([][]any{fact}), fixture.PgCastFactColumns, fixture.PgCastFactTypes, want)
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		pairs := [][]any{{int64(19), int64(25)}, {int64(25), int64(19)}, {int64(26), int64(2205)}, {int64(2205), int64(26)}, {int64(1560), int64(1562)}, {int64(1562), int64(1560)}, {int64(1560), int64(1560)}, {int64(1562), int64(1562)}}
		expectedPairs := make([][]any, 0, len(pairs))
		for _, fact := range fixture.PgCastOrdered(pgCastFacts()) {
			for _, pair := range pairs {
				if fact[0] == pair[0] && fact[1] == pair[1] {
					expectedPairs = append(expectedPairs, []any{fact[0], fact[1]})
				}
			}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT castsource,casttarget FROM pg_catalog.pg_cast"+fixture.PgCastPredicate(pairs)+" ORDER BY castsource,casttarget", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, expectedPairs))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN pgcast_domain AS INT", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pgcast_domain_seed(v pgcast_domain)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgcast_domain_seed VALUES(7)", 1))
		if b.PG {
			// PostgreSQL exposes domain identity through pg_typeof. Native
			// proves all domain omissions through its exact complete bag below.
			s = fixture.Select("SELECT castsource,casttarget FROM pg_catalog.pg_cast WHERE castsource=(SELECT pg_typeof(v)::oid FROM pgcast_domain_seed) OR casttarget=(SELECT pg_typeof(v)::oid FROM pgcast_domain_seed)", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, [][]any{})
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v::int4 AS base,7::int4::text AS words,7::int4::text::int4 AS roundtrip FROM pgcast_domain_seed", []string{"base", "words", "roundtrip"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "7", int64(7)}}))
		fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
	})
}
