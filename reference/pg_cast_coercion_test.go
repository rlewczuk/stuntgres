package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func pgCastCoercion(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
	run(referenceExec("CREATE TABLE pgcast_seed(i INT,b BIGINT,v VARCHAR(5),j JSON,jb JSONB,flag BOOL)", 0))
	run(referenceExec("INSERT INTO pgcast_seed VALUES(7,9,'abcd','{\"n\":1}','{\"n\":1}',true)", 1))
	pgCastCheckFacts(t, p, b, b.db, [][]any{
		{int64(23), int64(20), int64(481), "i", "f"},
		{int64(20), int64(23), int64(480), "a", "f"},
		{int64(23), int64(16), int64(2557), "e", "f"},
		{int64(16), int64(23), int64(2558), "e", "f"},
		{int64(16), int64(25), int64(2971), "a", "f"},
		{int64(114), int64(3802), int64(0), "a", "i"},
		{int64(3802), int64(114), int64(0), "a", "i"},
		{int64(1043), int64(1043), int64(669), "i", "f"},
	}, false)
	s := referenceSelect("SELECT CASE WHEN flag THEN i ELSE b END AS mixed,i::int8 AS wide,b::int4 AS narrow,i::bool AS truth,flag::int4 AS one,(-1)::int4::oid AS high,'4294967295'::oid::int4 AS signed,flag::text AS words,false::bool::text AS no,j::jsonb AS binary,jb::json AS textual,v::varchar(2) AS short,NULL::bool::text AS absent FROM pgcast_seed",
		[]string{"mixed", "wide", "narrow", "truth", "one", "high", "signed", "words", "no", "binary", "textual", "short", "absent"},
		[]string{"INT8", "INT8", "INT4", "BOOL", "INT4", "OID", "INT4", "TEXT", "TEXT", "JSONB", "JSON", "VARCHAR", "TEXT"},
		[][]any{{int64(7), int64(7), int64(9), true, int64(1), int64(4294967295), int64(-1), "true", "false", "{\"n\": 1}", "{\"n\": 1}", "ab", nil}})
	pgDescriptionRows(t, p, b, s)
	pgDescriptionRows(t, p, b, referencePrepared(s))
	run(referenceExec("CREATE TABLE pgcast_assignment(wide BIGINT,narrow INT,words TEXT,j JSON,jb JSONB)", 0))
	for _, prepared := range []bool{false, true} {
		s = referenceExec("INSERT INTO pgcast_assignment SELECT i,b,flag,jb,j FROM pgcast_seed", 1)
		s.prepared = prepared
		run(s)
	}
	run(referenceExec("INSERT INTO pgcast_assignment VALUES(NULL,NULL,NULL,NULL,NULL)", 1))
	s = referenceSelect("SELECT * FROM pgcast_assignment ORDER BY wide NULLS LAST", []string{"wide", "narrow", "words", "j", "jb"}, []string{"INT8", "INT4", "TEXT", "JSON", "JSONB"},
		[][]any{{int64(7), int64(9), "true", "{\"n\": 1}", "{\"n\": 1}"}, {int64(7), int64(9), "true", "{\"n\": 1}", "{\"n\": 1}"}, {nil, nil, nil, nil, nil}})
	run(s)
	run(referencePrepared(s))
	run(referenceExec("CREATE TABLE pgcast_short(v VARCHAR(2))", 0))
	run(referenceExec("INSERT INTO pgcast_short VALUES('ok')", 1))
	for _, prepared := range []bool{false, true} {
		s = referenceImplicitError("INSERT INTO pgcast_short SELECT v FROM pgcast_seed", referenceDiagnostic{Code: "22001", Message: "value too long for type character varying(2)"})
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		// A later input row fails after an earlier valid row was evaluated.
		s = referenceImplicitError("INSERT INTO pgcast_short VALUES('a'::varchar(5)),('abcd'::varchar(5))", referenceDiagnostic{Code: "22001", Message: "value too long for type character varying(2)"})
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(referenceSelect("SELECT * FROM pgcast_short", []string{"v"}, []string{"VARCHAR"}, [][]any{{"ok"}}))
	}
	run(referenceExec("CREATE TABLE pgcast_flags(flag BOOL)", 0))
	run(referenceExec("CREATE TABLE pgcast_ints(i INT)", 0))
	run(referenceExec("INSERT INTO pgcast_flags VALUES(false)", 1))
	run(referenceExec("INSERT INTO pgcast_ints VALUES(42)", 1))
	for _, tc := range []struct{ query, expr, column, target, source string }{
		{"INSERT INTO pgcast_flags SELECT i FROM pgcast_seed", "i FROM", "flag", "boolean", "integer"},
		{"INSERT INTO pgcast_ints SELECT flag FROM pgcast_seed", "flag FROM", "i", "integer", "boolean"},
	} {
		for _, prepared := range []bool{false, true} {
			want := referenceDiagnostic{Code: "42804", Message: "column \"" + tc.column + "\" is of type " + tc.target + " but expression is of type " + tc.source,
				Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(tc.query, tc.expr) + 1)}
			s = referenceImplicitError(tc.query, want)
			s.prepared, s.errorStage = prepared, "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			run(s)
			run(referenceSelect("SELECT * FROM pgcast_flags", []string{"flag"}, []string{"BOOL"}, [][]any{{false}}))
			run(referenceSelect("SELECT * FROM pgcast_ints", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
	}
	const heldQuery = "SELECT $1::int4::int8 AS wide,$2::bool::text AS words,$3::json::jsonb AS binary,$4::varchar(5)::varchar(2) AS short FROM pgcast_seed"
	stmt, err := b.db.PrepareContext(p.ctx, heldQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: heldQuery}
	for _, tc := range []struct {
		args []any
		row  []any
	}{
		{[]any{7, true, "{\"n\":1}", "abcd"}, []any{int64(7), "true", "{\"n\": 1}", "ab"}},
		{[]any{nil, nil, nil, nil}, []any{nil, nil, nil, nil}},
		{[]any{9, false, "{\"n\":2}", "xy"}, []any{int64(9), "false", "{\"n\": 2}", "xy"}},
	} {
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(heldQuery, []string{"wide", "words", "binary", "short"}, []string{"INT8", "TEXT", "JSONB", "VARCHAR"}, [][]any{tc.row}, tc.args...))
	}
	s = pgCastParameterError(heldQuery, "integer", "bad", "bad", true, "{}", "ok")
	referenceRunStep(t, p.ctx, held, b.recorder, s)
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(heldQuery, []string{"wide", "words", "binary", "short"}, []string{"INT8", "TEXT", "JSONB", "VARCHAR"}, [][]any{{int64(11), "true", "{}", "ok"}}, 11, true, "{}", "ok"))
}

func TestReferencePGCastCoercionClosure(t *testing.T) { pgCastScenario(t, pgCastCoercion) }

func TestReferencePGCastExclusions(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		absent := [][]any{{int64(23), int64(25)}, {int64(25), int64(23)}, {int64(701), int64(25)}, {int64(26), int64(25)}, {int64(25), int64(25)}, {int64(23), int64(23)}, {int64(16), int64(16)}}
		query := "SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast" + pgCastPredicate(absent)
		s := referenceSelect(query, pgCastFactColumns, pgCastFactTypes, [][]any{})
		pgDescriptionRows(t, p, b, s)
		pgDescriptionRows(t, p, b, referencePrepared(s))
		for _, fact := range [][]any{{int64(3802), int64(23), int64(3451), "e", "f"}, {int64(1083), int64(1186), int64(1370), "i", "f"}} {
			want := [][]any{}
			if b.pg {
				want = append(want, fact)
			}
			s = referenceSelect("SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast"+pgCastPredicate([][]any{fact}), pgCastFactColumns, pgCastFactTypes, want)
			pgDescriptionRows(t, p, b, s)
			pgDescriptionRows(t, p, b, referencePrepared(s))
		}
		if !b.pg {
			// NAME, REGCLASS, BIT/VARBIT and PG_NODE_TREE are partial native
			// providers, not an assertion that PostgreSQL lacks these rows.
			pairs := [][]any{{int64(19), int64(25)}, {int64(25), int64(19)}, {int64(26), int64(2205)}, {int64(2205), int64(26)}, {int64(1560), int64(1562)}, {int64(1562), int64(1560)}, {int64(1560), int64(1560)}, {int64(1562), int64(1562)}, {int64(194), int64(25)}, {int64(18), int64(25)}}
			pgDescriptionRows(t, p, b, referenceSelect("SELECT castsource,casttarget FROM pg_catalog.pg_cast"+pgCastPredicate(pairs), []string{"castsource", "casttarget"}, []string{"OID", "OID"}, [][]any{}))
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE DOMAIN pgcast_domain AS INT", 0))
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE pgcast_domain_seed(v pgcast_domain)", 0))
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO pgcast_domain_seed VALUES(7)", 1))
		if b.pg {
			// PostgreSQL exposes domain identity through pg_typeof. Native
			// proves all domain omissions through its exact complete bag below.
			s = referenceSelect("SELECT castsource,casttarget FROM pg_catalog.pg_cast WHERE castsource=(SELECT pg_typeof(v)::oid FROM pgcast_domain_seed) OR casttarget=(SELECT pg_typeof(v)::oid FROM pgcast_domain_seed)", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, [][]any{})
			pgDescriptionRows(t, p, b, s)
			pgDescriptionRows(t, p, b, referencePrepared(s))
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT v::int4 AS base,7::int4::text AS words,7::int4::text::int4 AS roundtrip FROM pgcast_domain_seed", []string{"base", "words", "roundtrip"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "7", int64(7)}}))
		pgCastCheckFacts(t, p, b, b.db, pgCastFacts(), true)
	})
}
