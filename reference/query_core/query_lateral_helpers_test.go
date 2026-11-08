package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
)

func lateralFixture(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE lat_t(id INT,v INT)", 0))
	run(fixture.Exec("INSERT INTO lat_t VALUES(1,2),(2,3),(3,NULL)", 3))
	run(fixture.Exec("CREATE TABLE lat_u(k INT,w TEXT)", 0))
	run(fixture.Exec("INSERT INTO lat_u VALUES(1,'a'),(1,'b'),(2,'c'),(4,'d')", 4))
}

// Positions count SQL characters, not UTF-8 bytes, including the Unicode prefix.
func lateralDiagnostic(q, token, code, message, detail, hint string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	if token != "" {
		at := strings.Index(q, token)
		if at < 0 {
			panic("missing lateral diagnostic token")
		}
		want.Position = fixture.Position(len([]rune(q[:at])) + 1)
	}
	want.Detail, want.Hint = detail, hint
	return want
}

func lateralLatestStep() fixture.Step {
	return fixture.Select("SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT u.w AS m FROM lat_u u WHERE u.k=t.id ORDER BY u.w DESC LIMIT 1) x ON true ORDER BY t.id", []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}, {int64(2), "c"}, {int64(3), nil}})
}

func lateralChainStep() fixture.Step {
	return fixture.Select("SELECT t.id,s.a,u.b FROM lat_t t,LATERAL (SELECT t.v AS a) s,LATERAL (SELECT s.a+1 AS b) u ORDER BY t.id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}, {int64(2), int64(3), int64(4)}, {int64(3), nil, nil}})
}

const lateralHeldSQL = "SELECT t.id,s.n FROM lat_t t,LATERAL (SELECT t.v+$1 AS n) s WHERE t.id=$2"

const lateralUncorrelatedDefinition = " SELECT t.id,\n    s.n\n   FROM lat_t t,\n    LATERAL ( SELECT 42 AS n) s;"

func lateralRepro(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE grin_lat_t (id int, v int)", 0))
	run(fixture.Exec("INSERT INTO grin_lat_t VALUES (1,2)", 1))
	// These two statements are the exact upstream reduced reproduction.
	run(fixture.Exec("CREATE VIEW grin_lat_v AS SELECT t.id, x.m FROM grin_lat_t t LEFT JOIN LATERAL (SELECT max(v) AS m FROM grin_lat_t) x ON true", 0))
	run(fixture.Exec("CREATE VIEW grin_lat_v2 AS SELECT t.id, f.family FROM grin_lat_t t CROSS JOIN LATERAL (VALUES ('a', t.v)) AS f(family, usage)", 0))
	subqueryBoth(run, fixture.Select("SELECT * FROM grin_lat_v ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	subqueryBoth(run, fixture.Select("SELECT * FROM grin_lat_v2 ORDER BY id", []string{"id", "family"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
	// The original second view projects only family. Exercise BOTH source columns.
	run(fixture.Exec("CREATE VIEW grin_lat_usage AS SELECT t.id,f.family,f.usage FROM grin_lat_t t CROSS JOIN LATERAL (VALUES ('a',t.v)) AS f(family,usage)", 0))
	subqueryBoth(run, fixture.Select("SELECT * FROM grin_lat_usage ORDER BY id", []string{"id", "family", "usage"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(2)}}))
}
