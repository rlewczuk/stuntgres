package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pseudoResultsSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c,
		"CREATE TABLE pseudo_result_rows(k integer)",
		"CREATE FUNCTION pseudo_result_pair(IN x anyelement,OUT first anyelement,OUT second anyelement) LANGUAGE SQL AS $$SELECT x,x$$",
		"CREATE FUNCTION pseudo_result_inout(INOUT first anyelement,OUT second anyelement) LANGUAGE SQL AS $$SELECT first,first$$",
		"CREATE FUNCTION pseudo_result_common(IN x anycompatible,IN y anycompatible,OUT first anycompatible,OUT second anycompatible) LANGUAGE SQL AS $$SELECT x,y$$",
		"CREATE FUNCTION pseudo_result_set(x anyelement,n integer) RETURNS SETOF anyelement LANGUAGE SQL AS $$SELECT x FROM pseudo_result_rows WHERE k <= n ORDER BY k$$",
		"CREATE FUNCTION pseudo_result_table(x anyelement,n integer) RETURNS TABLE(first anyelement,second anyelement) LANGUAGE SQL AS $$SELECT x,x FROM pseudo_result_rows WHERE k <= n ORDER BY k$$",
		"CREATE FUNCTION pseudo_result_empty(IN x anyelement,OUT first anyelement,OUT second anyelement) LANGUAGE SQL AS $$SELECT x,x WHERE false$$")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pseudo_result_rows VALUES(1),(2),(2)", 3))
}

func pseudoResultsOutputs(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, name := range []string{"pseudo_result_pair", "pseudo_result_inout"} {
		resultsBoth(t, p, b, c, "SELECT * FROM "+name+"(7)", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
		resultsBothMetadata(t, p, b, c, "SELECT * FROM "+name+"('x'::text)", []string{"first", "second"}, []string{"TEXT", "TEXT"}, [][]any{{"x", "x"}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata})
	}
	resultsBoth(t, p, b, c, "SELECT * FROM pseudo_result_common(1::integer,2::bigint)", []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(2)}})
	resultsBoth(t, p, b, c, "SELECT pseudo_result_pair(7) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{"(7,7)"}})
	resultsBoth(t, p, b, c, "SELECT pseudo_result_empty(7) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{nil}})
	resultsBoth(t, p, b, c, "SELECT * FROM pseudo_result_empty(7)", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}})
	q := "SELECT * FROM pseudo_result_pair($1::integer)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, value := range []any{int64(8), nil, int64(9)} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
			resultsStep(b, q, []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{value, value}}, value))
		resultsBothMetadata(t, p, b, c, "SELECT * FROM pseudo_result_pair('between'::text)", []string{"first", "second"}, []string{"TEXT", "TEXT"}, [][]any{{"between", "between"}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata})
	}
	resultsBothMetadata(t, p, b, c,
		"SELECT proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,prorettype::integer AS result,proretset AS sets FROM pg_proc WHERE proname='pseudo_result_pair'",
		[]string{"inputs", "alltypes", "modes", "names", "result", "sets"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"},
		[][]any{{"2283", "{2283,2283,2283}", "{i,o,o}", "{x,first,second}", int64(2249), false}},
		[]resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata})
}

func pseudoResultsSets(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for n, values := range [][][]any{{}, {{int64(7)}}, {{int64(7)}, {int64(7)}, {int64(7)}}} {
		resultsBoth(t, p, b, c, "SELECT v FROM pseudo_result_set(7,$1::integer) AS f(v) ORDER BY v", []string{"v"}, []string{"INT4"}, values, n)
	}
	resultsHeld(t, p, b, c, "SELECT v FROM pseudo_result_set($1::integer,$2::integer) AS f(v) ORDER BY v", []string{"v"}, []string{"INT4"},
		[][]any{{7, 2}, {8, 0}, {nil, 1}, {9, 1}}, [][][]any{{{int64(7)}, {int64(7)}, {int64(7)}}, {}, {{nil}}, {{int64(9)}}})
	resultsBothMetadata(t, p, b, c, "SELECT pseudo_result_set('x'::text,2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, []resultsNativeColumn{resultsTextMetadata})
	resultsBoth(t, p, b, c, "SELECT pseudo_result_set(7,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}})
	resultsBothMetadata(t, p, b, c, "SELECT * FROM pseudo_result_table('x'::text,2)", []string{"first", "second"}, []string{"TEXT", "TEXT"}, [][]any{{"x", "x"}, {"x", "x"}, {"x", "x"}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata})
	resultsBoth(t, p, b, c, "SELECT * FROM pseudo_result_table(7,0)", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{})
	resultsBoth(t, p, b, c, "SELECT * FROM pseudo_result_table(7,2)", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}, {int64(7), int64(7)}, {int64(7), int64(7)}})
}

func TestReferencePseudoTypesResultOutputs(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pseudoResultsSetup(t, p, b, c)
		pseudoResultsOutputs(t, p, b, c)
		want := fixture.ControlsReferenceDiagnostic("42P13", "cannot determine result data type")
		want.Detail = "A result of type anyelement requires at least one input of type anyelement, anyarray, anynonarray, anyenum, anyrange, or anymultirange."
		resultsError(t, p, c, "CREATE FUNCTION pseudo_result_bad(OUT first anyelement,OUT second anyelement) LANGUAGE SQL AS $$SELECT 1,1$$", false, "execute", want)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pseudo_result_bad'", 0)
	})
}

func TestReferencePseudoTypesResultSets(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pseudoResultsSetup(t, p, b, c)
		pseudoResultsSets(t, p, b, c)
	})
}

func TestReferencePseudoTypesResultPersistence(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pseudoResultsSetup(t, p, b, c)
		resultsExec(t, p, b, c, "CREATE VIEW pseudo_result_stored AS SELECT * FROM pseudo_result_table(7,2)")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			pseudoResultsOutputs(t, p, backend, conn)
			pseudoResultsSets(t, p, backend, conn)
			resultsStoredBoth(t, p, backend, conn, "SELECT first,second FROM pseudo_result_stored ORDER BY first,second", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}, {int64(7), int64(7)}, {int64(7), int64(7)}})
		}
		check(c, b)
		if b.PG {
			return
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT * FROM pseudo_result_pair(7)")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		emitted := resultsNativeReplayRound(t, p, b, c, check)
		if p.Postgres != nil {
			func() {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("unchanged polymorphic result dump replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}()
		}
		resultsMetadata(b, []string{"first", "second"}, []string{"INT4", "INT4"}, false)(t, retained)
		if !retained.Next() {
			t.Fatal("retained result lost", retained.Err())
		}
		var first, second int64
		if err := retained.Scan(&first, &second); err != nil || first != 7 || second != 7 || retained.Next() || retained.Err() != nil {
			t.Fatal("retained result changed", first, second, err, retained.Err())
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		defer recorded.Close()
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		check(clone, fixture.Backend{Recorder: rec})
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		invalid := before.String() + "; CREATE TABLE pseudo_result_prefix(v integer); CREATE FUNCTION pseudo_result_bad(OUT first anyelement,OUT second anyelement) LANGUAGE SQL AS $$SELECT 1,1$$;"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid polymorphic result restore accepted")
		}
		check(c, b)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='pseudo_result_prefix'", 0)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed restore changed result definitions/data", err)
		}
	})
}

func TestReferencePseudoTypesResultRestrictedOutputs(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, input, output, argument, typ, inputs, alltypes, message string
			value                                                      any
		}{
			{"enum", "anyelement", "anyenum", "1", "INT4", "2283", "{2283,3500,23}", "type matched to anyenum is not an enum type: integer", int64(1)},
			{"nonarray", "anyelement", "anynonarray", "ARRAY[1]", "_INT4", "2283", "{2283,2776,23}", "type matched to anynonarray is an array type: integer[]", "{1}"},
			{"common_nonarray", "anycompatible", "anycompatiblenonarray", "ARRAY[1]", "_INT4", "5077", "{5077,5079,23}", "type matched to anycompatiblenonarray is an array type: integer[]", "{1}"},
		} {
			name := "pseudo_result_restricted_" + tc.name
			resultsExec(t, p, b, c,
				"CREATE FUNCTION "+name+"(IN x "+tc.input+",OUT a "+tc.output+",OUT b integer) LANGUAGE SQL AS $$SELECT x,1$$",
				"CREATE FUNCTION "+name+"_scalar(x "+tc.input+") RETURNS "+tc.output+" LANGUAGE SQL AS $$SELECT x$$")
			resultsBoth(t, p, b, c, "SELECT * FROM "+name+"("+tc.argument+")",
				[]string{"a", "b"}, []string{tc.typ, "INT4"}, [][]any{{tc.value, int64(1)}})
			resultsBothMetadata(t, p, b, c,
				"SELECT proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,prorettype::integer AS result,proretset AS sets FROM pg_proc WHERE proname='"+name+"'",
				[]string{"inputs", "alltypes", "modes", "names", "result", "sets"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"},
				[][]any{{tc.inputs, tc.alltypes, "{i,o,o}", "{x,a,b}", int64(2249), false}},
				[]resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata})
			want := fixture.ControlsReferenceDiagnostic("42804", tc.message)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+name+"_scalar("+tc.argument+")", want)
				s.SelectRows, s.Prepared = true, prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferencePseudoTypesRecordSingleScalar(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c,
			"CREATE FUNCTION pseudo_one() RETURNS record LANGUAGE SQL AS $$SELECT 7 AS only_member$$",
			"CREATE FUNCTION pseudo_one_null() RETURNS record LANGUAGE SQL AS $$SELECT NULL::integer AS only_member$$",
			"CREATE FUNCTION pseudo_one_empty() RETURNS record LANGUAGE SQL AS $$SELECT 7 AS only_member WHERE false$$")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, tc := range []struct {
				name   string
				scalar any
				member any
			}{
				{"pseudo_one", "(7)", int64(7)},
				{"pseudo_one_null", "()", nil},
				{"pseudo_one_empty", nil, nil},
			} {
				q := "SELECT " + tc.name + "() AS v"
				s := resultsStep(backend, q, []string{"v"}, []string{"RECORD"}, [][]any{{tc.scalar}})
				s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
					t.Helper()
					// A present tuple containing one NULL is not an absent
					// terminal row. Check the raw distinction before normalization.
					if tc.scalar == nil {
						if value != nil {
							t.Fatalf("%s absent record got %T %#v, want SQL NULL", tc.name, value, value)
						}
					} else if backend.PG {
						raw, ok := value.([]byte)
						if !ok || raw == nil || !bytes.Equal(raw, []byte(tc.scalar.(string))) {
							t.Fatalf("%s present record got %T %#v, want nonnil bytes %q", tc.name, value, value, tc.scalar)
						}
					} else if raw, ok := value.(string); !ok || raw != tc.scalar {
						t.Fatalf("%s present record got %T %#v, want string %q", tc.name, value, value, tc.scalar)
					}
					return false // Retain the fixture's canonical codec check.
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
				resultsBoth(t, p, backend, conn, "SELECT * FROM "+tc.name+"() AS t(only_member integer)",
					[]string{"only_member"}, []string{"INT4"}, [][]any{{tc.member}})
			}
			// Caller completion and scalar execution must not specialize the
			// persistent declaration into a concrete or set-returning result.
			resultsBothMetadata(t, p, backend, conn,
				"SELECT proname::text AS name,prorettype::integer AS result,proretset AS sets,pronargs::integer AS inputs,proargtypes::text AS inputtypes,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names FROM pg_proc WHERE proname IN ('pseudo_one','pseudo_one_null','pseudo_one_empty') ORDER BY proname",
				[]string{"name", "result", "sets", "inputs", "inputtypes", "alltypes", "modes", "names"},
				[]string{"TEXT", "INT4", "BOOL", "INT4", "TEXT", "TEXT", "TEXT", "TEXT"},
				[][]any{
					{"pseudo_one", int64(2249), false, int64(0), "", nil, nil, nil},
					{"pseudo_one_empty", int64(2249), false, int64(0), "", nil, nil, nil},
					{"pseudo_one_null", int64(2249), false, int64(0), "", nil, nil, nil},
				},
				[]resultsNativeColumn{resultsTextMetadata, {}, resultsNonNullMetadata, {}, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata})
		}
		check(c, b)
		resultsHeld(t, p, b, c, "SELECT * FROM pseudo_one_null() AS t(only_member integer)",
			[]string{"only_member"}, []string{"INT4"}, [][]any{{}, {}}, [][][]any{{{nil}}, {{nil}}})
		if b.PG {
			return
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT pseudo_one_null() AS present,pseudo_one_empty() AS absent")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		emitted := resultsNativeReplayRound(t, p, b, c, check)
		var restored bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil || !bytes.Equal(emitted, restored.Bytes()) {
			t.Fatal("same-database single-scalar record restore changed dump bytes", err)
		}
		if p.Postgres != nil {
			func() {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("unchanged single-scalar record dump replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}()
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		defer recorded.Close()
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		check(clone, fixture.Backend{Recorder: rec})
		var cloned bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, raw, &cloned); err != nil || !bytes.Equal(emitted, cloned.Bytes()) {
			t.Fatal("independent-driver single-scalar record restore changed dump bytes", err)
		}
		resultsMetadata(b, []string{"present", "absent"}, []string{"RECORD", "RECORD"}, false)(t, retained)
		if !retained.Next() {
			t.Fatal("retained single-scalar records lost", retained.Err())
		}
		var present, absent any
		if err := retained.Scan(&present, &absent); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, false, "RECORD", present)
		if present != "()" || absent != nil {
			t.Fatalf("retained present NULL tuple/absent record changed: %T %#v / %T %#v", present, present, absent, absent)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained single-scalar record cardinality", retained.Err())
		}
	})
}
