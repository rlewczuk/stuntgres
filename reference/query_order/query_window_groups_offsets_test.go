package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowGroupsOffsets(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE DOMAIN groups_offset AS NUMERIC", 0))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE offset_source(v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO offset_source VALUES(1)", 1))
		for _, offset := range []string{"'1'", "1::SMALLINT", "1::BIGINT", "(1+0)", "(SELECT 1)", "(SELECT v FROM offset_source)", "$1"} {
			s := queryWindowGroupsResult(offset+" PRECEDING", 2, 2, 3, 3, 3)
			if offset == "$1" {
				s.Args = []any{1}
			}
			queryWindowRoutes(t, p, b, s)
		}
		// Assignment coercion rounds numeric to bigint; not a RANGE carrier rule.
		for _, offset := range []string{"1.5", "1.5::groups_offset", "$1::NUMERIC"} {
			s := queryWindowGroupsResult(offset+" PRECEDING", 2, 2, 3, 5, 5)
			if offset == "$1::NUMERIC" {
				s.Args = []any{"1.5"}
			}
			queryWindowRoutes(t, p, b, s)
		}
		for _, frame := range []string{"BETWEEN $1 PRECEDING AND $2 FOLLOWING", "BETWEEN $1 PRECEDING AND $1 FOLLOWING"} {
			s := queryWindowGroupsResult(frame, 3, 3, 5, 3, 3)
			s.Args = []any{1, 1}
			if frame == "BETWEEN $1 PRECEDING AND $1 FOLLOWING" {
				s.Args = []any{1}
			}
			queryWindowRoutes(t, p, b, s)
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT $1::INT AS x,count(*) OVER(ORDER BY k GROUPS $1 PRECEDING) AS n FROM g ORDER BY id", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}, {int64(1), int64(3)}, {int64(1), int64(3)}, {int64(1), int64(3)}}, 1))
		queryWindowRangeAnalysis(t, p, b, "SELECT $1::BOOL,count(*) OVER(ORDER BY k GROUPS $1 PRECEDING) FROM g", "42804", "argument of GROUPS must be type bigint, not type boolean", "$1 PRECEDING", "", true)
		// Outer-level Vars are legal in an inner frame; the count input is a
		// singleton and each outer offset is positive, so every answer is one.
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,(SELECT count(*) OVER(ORDER BY 1 GROUPS g.k PRECEDING)) AS n FROM g ORDER BY id", 1, 1, 1, 1, 1))
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k GROUPS (SELECT v FROM offset_source WHERE FALSE) PRECEDING) FROM g", "22004", "frame starting offset must not be null")
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO offset_source VALUES(2)", 1))
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k GROUPS (SELECT v FROM offset_source) PRECEDING) FROM g", "21000", "more than one row returned by a subquery used as an expression")
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k GROUPS (9223372036854775807::BIGINT+1) PRECEDING) FROM g", "22003", "bigint out of range")
		for _, tc := range []struct{ frame, code, message string }{
			{"NULL PRECEDING", "22004", "frame starting offset must not be null"},
			{"-1 PRECEDING", "22013", "frame starting offset must not be negative"},
			{"BETWEEN 1 PRECEDING AND NULL FOLLOWING", "22004", "frame ending offset must not be null"},
			{"BETWEEN 1 PRECEDING AND -1 FOLLOWING", "22013", "frame ending offset must not be negative"},
			{"BETWEEN -1 PRECEDING AND NULL FOLLOWING", "22013", "frame starting offset must not be negative"},
			{"BETWEEN NULL PRECEDING AND -1 FOLLOWING", "22004", "frame starting offset must not be null"},
			{"(SELECT 1/0) PRECEDING", "22012", "division by zero"},
		} {
			queryWindowRangeRuntime(t, p, b, queryWindowGroupsResult(tc.frame).Query, tc.code, tc.message)
		}
	})
}

func TestReferenceQueryWindowGroupsPrepared(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		q := queryWindowGroupsResult("$1 PRECEDING").Query
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, tc := range []struct {
			arg           any
			values        []int64
			code, message string
		}{
			{1, []int64{2, 2, 3, 3, 3}, "", ""},
			{0, []int64{2, 2, 1, 2, 2}, "", ""},
			{2, []int64{2, 2, 3, 5, 5}, "", ""},
			{nil, nil, "22004", "frame starting offset must not be null"},
			{-1, nil, "22013", "frame starting offset must not be negative"},
			{1, []int64{2, 2, 3, 3, 3}, "", ""},
		} {
			s := queryWindowRangeResult(q, tc.values...)
			s.Args = []any{tc.arg}
			if tc.code != "" {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.arg)
				s.SelectRows = true
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		q = queryWindowGroupsResult("BETWEEN $1 PRECEDING AND $2 FOLLOWING").Query
		stmt2, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt2.Close()
		held2 := fixture.HeldStatement{b.DB, stmt2, q}
		for _, tc := range []struct {
			start, end    any
			values        []int64
			code, message string
		}{
			{1, 1, []int64{3, 3, 5, 3, 3}, "", ""},
			{0, 0, []int64{2, 2, 1, 2, 2}, "", ""},
			{1, nil, nil, "22004", "frame ending offset must not be null"},
			{1, -1, nil, "22013", "frame ending offset must not be negative"},
			{-1, nil, nil, "22013", "frame starting offset must not be negative"},
			{1, 1, []int64{3, 3, 5, 3, 3}, "", ""},
		} {
			s := queryWindowRangeResult(q, tc.values...)
			s.Args = []any{tc.start, tc.end}
			if tc.code != "" {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.start, tc.end)
				s.SelectRows = true
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, held2, b.Recorder, s)
		}
	})
}

// Pinned discovery confirmed these GROUPS-specific demand expectations. Empty
// input still enters WindowAgg offset validation; LIMIT 0 and rn-only do not.
func TestReferenceQueryWindowGroupsDemand(t *testing.T) {
	for _, input := range []string{"finite", "empty", "nulls"} {
		for _, endpoint := range []string{"starting", "ending"} {
			for _, offset := range []string{"NULL", "-1"} {
				t.Run(input+"/"+endpoint+"/"+offset, func(t *testing.T) {
					queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
						queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE demand(id INT,k INT)", 0))
						if input == "finite" {
							queryWindowRun(t, p, b, fixture.Exec("INSERT INTO demand VALUES(1,1),(2,2)", 2))
						}
						if input == "nulls" {
							queryWindowRun(t, p, b, fixture.Exec("INSERT INTO demand VALUES(1,NULL),(2,NULL)", 2))
						}
						frame := "BETWEEN " + offset + " PRECEDING AND CURRENT ROW"
						if endpoint == "ending" {
							frame = "BETWEEN UNBOUNDED PRECEDING AND " + offset + " FOLLOWING"
						}
						code, suffix := "22004", "null"
						if offset == "-1" {
							code, suffix = "22013", "negative"
						}
						message := "frame " + endpoint + " offset must not be " + suffix
						q := "SELECT id,count(*) OVER(ORDER BY k GROUPS " + frame + ") AS n FROM demand ORDER BY id"
						queryWindowRangeRuntime(t, p, b, q, code, message)
						queryWindowRoutes(t, p, b, fixture.Select(q+" LIMIT 0", []string{"id", "n"}, []string{"INT4", "INT8"}, nil))
						queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER(ORDER BY k GROUPS "+frame+") AS n FROM demand ORDER BY n", []string{"n"}, []string{"INT8"}, rangeDemandIDs(input)))
						queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM demand WINDOW unused AS(ORDER BY k GROUPS "+frame+") ORDER BY id", []string{"id"}, []string{"INT4"}, rangeDemandIDs(input)))
						queryWindowRangeRuntime(t, p, b, "SELECT EXISTS(SELECT count(*) OVER(ORDER BY k GROUPS "+frame+") FROM demand) AS found", code, message)
					})
				})
			}
		}
	}
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE groups_once START WITH 1", 0))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(PARTITION BY k ORDER BY id GROUPS nextval('groups_once') PRECEDING) AS n FROM g ORDER BY id", 1, 2, 1, 1, 2))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT last_value FROM groups_once", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM g WINDOW unused AS(ORDER BY k GROUPS nextval('groups_once') PRECEDING) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT last_value FROM groups_once", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
