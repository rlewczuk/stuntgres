package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeInspectFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"inspect_smoke_reader"})
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		declarations := []string{"id integer"}
		finite := []string{"1"}
		empty := []string{"2"}
		null := []string{"3"}
		for i, f := range inspectFamilies {
			column := "r" + string(rune('a'+i))
			declarations = append(declarations, column+" "+f.name)
			finite = append(finite, rangeLiteral(f.finite))
			empty = append(empty, "'empty'")
			null = append(null, "NULL")
		}
		for i, f := range inspectFamilies[:3] {
			column := "m" + string(rune('a'+i))
			declarations = append(declarations, column+" "+strings.Replace(f.name, "range", "multirange", 1))
			finite = append(finite, "'{[8,9),[1,3)}'")
			empty = append(empty, "'{}'")
			null = append(null, "NULL")
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_smoke("+strings.Join(declarations, ",")+")", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inspect_smoke VALUES ("+strings.Join(finite, ",")+"),("+strings.Join(empty, ",")+"),("+strings.Join(null, ",")+")", 3))
		for i, f := range inspectFamilies {
			column := "r" + string(rune('a'+i))
			li, ui := true, false
			if f.name == "numrange" {
				li, ui = false, true
			}
			if f.name == "tsrange" || f.name == "tstzrange" {
				ui = true
			}
			s := inspectTupleStep(b, column, f.subtype, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false})
			s.Query += " FROM inspect_smoke WHERE id=1"
			inspectRunBoth(t, p, b, s)
			s.Query = inspectTupleQuery(column) + " FROM inspect_smoke WHERE id=2"
			s.Rows = [][]any{{nil, nil, true, false, false, false, false}}
			inspectRunBoth(t, p, b, s)
			s.Query = inspectTupleQuery(column) + " FROM inspect_smoke WHERE id=3"
			s.Rows = [][]any{{nil, nil, nil, nil, nil, nil, nil}}
			inspectRunBoth(t, p, b, s)
			s.Query = inspectTupleQuery(column) + " FROM inspect_smoke WHERE id=99"
			s.Rows = [][]any{}
			inspectRunBoth(t, p, b, s)
			h := inspectHullStep(b, "range_merge("+column+","+column+")", f.name, f.hull)
			h.Query += " FROM inspect_smoke WHERE id=1"
			inspectRunBoth(t, p, b, h)
		}
		for i, f := range inspectFamilies[:3] {
			column := "m" + string(rune('a'+i))
			s := inspectTupleStep(b, column, f.subtype, []any{inspectBound(b, f.subtype, "1"), inspectBound(b, f.subtype, "9"), false, true, false, false, false})
			s.Query += " FROM inspect_smoke WHERE id=1"
			inspectRunBoth(t, p, b, s)
			for _, tc := range []struct {
				id   string
				want any
			}{{"1", "[1,9)"}, {"2", "empty"}, {"3", nil}} {
				h := inspectHullStep(b, "range_merge("+column+")", f.name, tc.want)
				h.Query += " FROM inspect_smoke WHERE id=" + tc.id
				inspectRunBoth(t, p, b, h)
			}
		}
		inspectInputTuple(t, p, b, "numrange", "NUMERIC", "(,)", []any{nil, nil, false, false, false, true, true})
		inspectInputTuple(t, p, b, "numrange", "NUMERIC", "[-Infinity,Infinity]", []any{"-Infinity", "Infinity", false, true, true, false, false})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_smoke_bind(v int8range)", 0))
		q := "INSERT INTO inspect_smoke_bind VALUES ($1)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input any
			row   []any
		}{{"(1,4]", []any{int64(2), int64(5), false, true, false, false, false}}, {"empty", []any{nil, nil, true, false, false, false, false}}, {nil, []any{nil, nil, nil, nil, nil, nil, nil}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, tc.input))
			s := inspectTupleStep(b, "v", "INT8", tc.row)
			s.Query += " FROM inspect_smoke_bind"
			inspectRunBoth(t, p, b, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM inspect_smoke_bind", 1))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE inspect_smoke_bind", 0))
		inspectInputHull(t, p, b, "int4range", "int4range", "[1,2)", "[3,4)", "[1,4)", false)
		inspectRunBoth(t, p, b, inspectSelect(b, "SELECT lower('AbC') AS lo,upper('AbC') AS hi", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{"abc", "ABC"}}))
		q = "SELECT isempty(1)"
		s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42883", "function isempty(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "isempty", "", ""))
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		for _, tc := range []struct{ q, code, message, hint, at string }{
			{"SELECT lower(x => 'AbC')", "42883", "function lower(x => unknown) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "lower"},
			{"SELECT absent.isempty(missing)", "42703", `column "missing" does not exist`, "", "missing"},
			{"SELECT lower('AbC') FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "", "1"},
		} {
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(tc.q, inspectDiagnostic(tc.q, tc.code, tc.message, tc.hint, tc.at, "", ""))
				step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		}
		inspectCanceled(t, p, b)
		// Finish ACL witnesses on their owned relation before Dump/Restore replaces
		// catalogs. The control helper's shape-changing target is a separate Driver.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v int4range)", 0))
		inspectAuthorization(t, p, b, "inspect_smoke_reader")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE r", 0))
		checks := inspectStoredRecipes(t, p, b)
		rangeControlCycle(t, p, b, checks, make([][]bool, len(checks)), nil, inspectReplayStep)
	})
}
