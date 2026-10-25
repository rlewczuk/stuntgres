package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeInspectMetadata(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, f := range inspectFamilies {
			names := []string{f.name}
			if f.name == "int4range" || f.name == "int8range" || f.name == "numrange" {
				names = append(names, strings.Replace(f.name, "range", "multirange", 1))
			}
			for _, name := range names {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v "+name+")", 0))
				s := inspectTupleStep(b, "v", f.subtype, []any{nil, nil, nil, nil, nil, nil, nil})
				s.Query += " FROM r"
				s.Rows = [][]any{}
				inspectRunBoth(t, p, b, s)
				hull := "range_merge(v,v)"
				if strings.Contains(name, "multirange") {
					hull = "range_merge(v)"
				}
				h := inspectHullStep(b, hull, f.name, nil)
				h.Query += " FROM r"
				h.Rows = [][]any{}
				inspectRunBoth(t, p, b, h)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (NULL)", 1))
				s.Rows = [][]any{{nil, nil, nil, nil, nil, nil, nil}}
				h.Rows = [][]any{{nil}}
				inspectRunBoth(t, p, b, s)
				inspectRunBoth(t, p, b, h)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE r", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN inspect_domain AS "+name, 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN inspect_nested AS inspect_domain", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v inspect_nested)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (NULL)", 1))
				s = inspectTupleStep(b, "v", f.subtype, []any{nil, nil, nil, nil, nil, nil, nil})
				s.Query += " FROM r"
				inspectRunBoth(t, p, b, s)
				if strings.Contains(name, "multirange") {
					hull = "range_merge(v)"
				} else {
					hull = "range_merge(v,v)"
				}
				h = inspectHullStep(b, hull, f.name, nil)
				h.Query += " FROM r"
				inspectRunBoth(t, p, b, h)
				finite := f.finite
				if strings.Contains(name, "multirange") {
					finite = "{" + finite + "}"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1)", 1, finite)))
				li, ui := true, false
				if f.name == "numrange" {
					li, ui = false, true
				}
				if f.name == "tsrange" || f.name == "tstzrange" {
					ui = true
				}
				s = inspectTupleStep(b, "v", f.subtype, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false})
				s.Query += " FROM r WHERE v IS NOT NULL"
				inspectRunBoth(t, p, b, s)
				h = inspectHullStep(b, hull, f.name, f.hull)
				h.Query += " FROM r WHERE v IS NOT NULL"
				inspectRunBoth(t, p, b, h)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE r", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN inspect_nested", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN inspect_domain", 0))
			}
		}
		for _, tc := range []struct {
			signature string
			oid       int64
		}{
			{"lower(anyrange)", 3848}, {"upper(anyrange)", 3849}, {"isempty(anyrange)", 3850}, {"lower_inc(anyrange)", 3851}, {"upper_inc(anyrange)", 3852}, {"lower_inf(anyrange)", 3853}, {"upper_inf(anyrange)", 3854},
			{"range_merge(anyrange,anyrange)", 4057}, {"range_merge(anymultirange)", 4228}, {"lower(anymultirange)", 4235}, {"upper(anymultirange)", 4236}, {"isempty(anymultirange)", 4237}, {"lower_inc(anymultirange)", 4238}, {"upper_inc(anymultirange)", 4239}, {"lower_inf(anymultirange)", 4240}, {"upper_inf(anymultirange)", 4241},
		} {
			inspectRunBoth(t, p, b, fixture.Select("SELECT "+rangeLiteral(tc.signature)+"::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{tc.oid}}))
		}
	})
}

func TestReferenceRangeInspectResolution(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q   string
			row []any
		}{
			{"SELECT lower('AbC') AS lo,upper('AbC') AS hi", []any{"abc", "ABC"}},
			{"SELECT lower('[1,4)') AS lo,upper('[1,4)') AS hi", []any{"[1,4)", "[1,4)"}},
			{"SELECT lower(NULL) AS lo,upper(NULL) AS hi", []any{nil, nil}},
		} {
			inspectRunBoth(t, p, b, inspectSelect(b, tc.q, []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{tc.row}))
		}
		for _, name := range []string{"lower", "upper"} {
			q := "SELECT " + name + "($1) AS v"
			s := inspectSelect(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}})
			if name == "upper" {
				s.Rows = [][]any{{"ABC"}}
			}
			s.Args = []any{"AbC"}
			inspectRunBoth(t, p, b, s)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v int4range)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES ('[1,2)')", 1))
		s := inspectHullStep(b, "range_merge(v,$1)", "int4range", "[1,4)")
		s.Query += " FROM r"
		s.Args = []any{"[3,4)"}
		inspectRunBoth(t, p, b, s)
		inspectRunBoth(t, p, b, inspectSelect(b, "SELECT pg_catalog.lower(v) AS lo FROM r", []string{"lo"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
