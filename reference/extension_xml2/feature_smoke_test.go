package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2FeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xml2_probe_user", "xml2_probe_denied", "xml2_provider_owner"})
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xml2Inventory(t, p, b, c)
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid('<r><a>12</a><a>7</a></r>') AS v,xpath_string('<r><a>12</a><a>7</a></r>','/r/a') AS s,xpath_number('<r><a>12</a><a>7</a></r>','/r/a') AS n,xpath_bool('<r><a>12</a><a>7</a></r>','/r/a') AS b",
			[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{true, "12", float64(12), true}}))
		xml2Held(t, p, b, c,
			xml2Scalars("<r><a>12</a></r>", "a", "12", float64(12), true),
			xml2Scalars("<r><a>12</a></r>", "r/a", "", nil, false),
			xml2Scalars("<r/>", "/missing", "", nil, false),
			xml2Scalars(nil, "[", nil, nil, nil),
			xml2Scalars("<r>", "[", nil, nil, false),
			xml2Scalars("<r><a>12</a></r>", "a", "12", float64(12), true))
		xml2Both(t, p, b, c, fixture.Select("SELECT xpath_string('<r>A<b>B</b>C</r>',' ') AS s,xpath_number('<r/>','16777217') AS n,xpath_number('<r/>','0 div 0') AS nan",
			[]string{"s", "n", "nan"}, []string{"TEXT", "FLOAT4", "FLOAT4"}, [][]any{{"ABC", float64(16777216), nil}}))
		q := "SELECT xml_valid($1::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmloption TO "+mode, 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{mode == "CONTENT"}}, "plain text"))
			xml2Both(t, p, b, c, xml2Scalars("plain text", ".", nil, nil, false))
		}
		xml2Consumers(t, p, b, c)
		xml2Saved(t, p, b, c)
		xml2Guard(t, p, b, c, "public", "xpath_string")
		xml2ACLSetup(t, p, b, c)
		xml2SchemaAuthority(t, p, b, c)
		xml2Demand(t, p, b, c)
		xml2ValidationWarnings(t, p, b)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_smoke_atomic(id integer,n real)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_smoke_atomic VALUES(0,99)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_smoke_source(id integer,doc text,path text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_smoke_source VALUES(1,'<r>12</r>','.'),(2,'<r/>','[')", 2))
		_, err = c.ExecContext(p.Ctx, "INSERT INTO xml2_smoke_atomic SELECT id,xpath_number(doc,path) FROM xml2_smoke_source ORDER BY id")
		xml2Syntax(t, err)
		xml2Both(t, p, b, c, fixture.Select("SELECT id,n FROM xml2_smoke_atomic ORDER BY id", []string{"id", "n"}, []string{"INT4", "FLOAT4"}, [][]any{{int64(0), float64(99)}}))
		xml2ControlRoundTrip(t, p, b, c)
	})
}
