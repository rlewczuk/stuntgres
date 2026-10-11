package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowGroupsViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE DOMAIN groups_literal AS BIGINT", 0),
		fixture.Exec("CREATE TABLE offset_source(v INT)", 0),
		fixture.Exec("INSERT INTO offset_source VALUES(1)", 1),
		fixture.Exec("CREATE VIEW groups_inline AS SELECT id,count(*) OVER(ORDER BY k GROUPS 1::BIGINT PRECEDING) AS n FROM g", 0),
		fixture.Exec("CREATE VIEW groups_named AS SELECT id,count(*) OVER w AS n FROM g WINDOW base AS(ORDER BY k),w AS(base GROUPS '1' PRECEDING),unused AS(ORDER BY k GROUPS (SELECT v FROM offset_source) PRECEDING)", 0),
		fixture.Exec("CREATE VIEW groups_subquery AS SELECT id,count(*) OVER(ORDER BY k GROUPS (SELECT v FROM offset_source) PRECEDING) AS n FROM g", 0),
		fixture.Exec("CREATE VIEW groups_domain AS SELECT id,count(*) OVER w AS n FROM g WINDOW w AS(ORDER BY k GROUPS '1'::groups_literal PRECEDING)", 0),
		fixture.Exec("CREATE VIEW groups_collision(k,id) AS SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING),id FROM g", 0),
		fixture.Exec("ALTER TABLE g RENAME COLUMN k TO renamed", 0),
		fixture.Exec("ALTER TABLE offset_source RENAME COLUMN v TO renamed", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	var steps []fixture.Step
	for _, name := range []string{"groups_inline", "groups_named", "groups_subquery", "groups_domain"} {
		s := queryWindowRangeResult("SELECT id,n FROM "+name+" ORDER BY id", 2, 2, 3, 3, 3)
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, queryWindowReviewReplay(t, p, b, name, s)...)
	}
	collision := fixture.Select("SELECT k,id FROM groups_collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(3), int64(4)}, {int64(3), int64(5)}})
	queryWindowRoutes(t, p, b, collision)
	steps = append(steps, collision)
	if !b.PG {
		// Real native Dump, pristine different fixture Restore, malformed/canceled
		// Restore preservation, and held cursor across Reset/Restore. SQL replay
		// above is portable coverage, not a substitute for these controls.
		queryWindowRangeControls(t, p, steps)
	}
}

func TestReferenceQueryWindowGroupsViews(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowGroupsViews(t, p, b)
	})
}

func TestReferenceQueryWindowGroupsNamedOffsetIdentity(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run("distinct="+strconv.FormatBool(distinct)+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE SEQUENCE groups_identity START WITH 1", 0),
						fixture.Exec("CREATE TABLE identity_t(id INT,k INT)", 0),
						fixture.Exec("INSERT INTO identity_t VALUES(1,1),(2,2),(3,3)", 3),
					} {
						queryWindowRun(t, p, b, s)
					}
					second := "w"
					definitions := "w AS(ORDER BY k GROUPS nextval('groups_identity') PRECEDING)"
					upper, last := int64(2), int64(1)
					if distinct {
						second = "v"
						definitions += ",v AS(ORDER BY k GROUPS nextval('groups_identity') PRECEDING)"
						upper, last = 3, 2
					}
					q := "SELECT id,LEAST(a,b) AS lo,GREATEST(a,b) AS hi FROM (SELECT id,count(*) OVER w AS a,count(*) OVER " + second + " AS b FROM identity_t WINDOW " + definitions + ") AS frames ORDER BY id"
					s := fixture.Select(q, []string{"id", "lo", "hi"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(2), upper}})
					s.Prepared = prepared
					queryWindowRun(t, p, b, s)
					queryWindowRoutes(t, p, b, fixture.Select("SELECT last_value FROM groups_identity", []string{"last_value"}, []string{"INT8"}, [][]any{{last}}))
				})
			})
		}
	}
}

func TestReferenceQueryWindowGroupsNamedOffsetIdentityPersistence(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SEQUENCE groups_identity START WITH 1", 0),
			fixture.Exec("CREATE TABLE identity_t(id INT,k INT)", 0),
			fixture.Exec("INSERT INTO identity_t VALUES(1,1),(2,2),(3,3)", 3),
			fixture.Exec("CREATE VIEW groups_identity_view AS SELECT id,LEAST(count(*) OVER w,count(*) OVER v) AS lo,GREATEST(count(*) OVER w,count(*) OVER v) AS hi FROM identity_t WINDOW w AS(ORDER BY k GROUPS (1+nextval('groups_identity')%2) PRECEDING),v AS(ORDER BY k GROUPS (1+nextval('groups_identity')%2) PRECEDING)", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		expected := fixture.Select("SELECT * FROM groups_identity_view ORDER BY id", []string{"id", "lo", "hi"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(2), int64(3)}})
		queryWindowRoutes(t, p, b, expected)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT last_value FROM groups_identity", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
		steps := queryWindowReviewReplay(t, p, b, "groups_identity_view", expected)
		if !b.PG {
			queryWindowRangeControls(t, p, steps)
		}
	})
}

func TestReferenceQueryWindowGroupsFeatureSmoke(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowGroupsResult("1 PRECEDING", 2, 2, 3, 3, 3))
		queryWindowRoutes(t, p, b, queryWindowGroupsResult("CURRENT ROW", 2, 2, 1, 2, 2))
		queryWindowRoutes(t, p, b, queryWindowGroupsResult("BETWEEN 1 FOLLOWING AND 1 FOLLOWING", 1, 1, 2, 0, 0))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER w AS n FROM g WINDOW base AS(ORDER BY k),w AS(base GROUPS 1 PRECEDING) ORDER BY id", 2, 2, 3, 3, 3))
		queryWindowRangeAnalysis(t, p, b, "SELECT count(*) OVER(GROUPS 1 PRECEDING) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", "")
		queryWindowRangeRuntime(t, p, b, queryWindowGroupsResult("-1 PRECEDING").Query, "22013", "frame starting offset must not be negative")
		q := queryWindowGroupsResult("$1 PRECEDING").Query
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		s := queryWindowRangeResult(q, 2, 2, 1, 2, 2)
		s.Args = []any{0}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22013", Message: "frame starting offset must not be negative"}, -1)
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		s = queryWindowRangeResult(q, 2, 2, 3, 3, 3)
		s.Args = []any{1}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		queryWindowGroupsMetadata(t, p, b)
		queryWindowGroupsViews(t, p, b)
	})
}
