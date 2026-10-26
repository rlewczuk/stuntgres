package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func regtsError(q, code, primary, position, portal string, args ...any) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic(code, primary)
	d.Position, d.Context = position, portal
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	return s
}
func regtsDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, alias, input, code, primary string) {
	t.Helper()
	q := "SELECT " + regtsLiteral(input) + "::" + alias
	s := regtsError(q, code, primary, "8", "")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	q = "SELECT $1::" + alias
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("parameter Prepare", err)
	}
	defer stmt.Close()
	portal := "unnamed portal parameter $1 = '...'"
	if input == "" {
		portal = "unnamed portal parameter $1 = ''"
	}
	s = regtsError(q, code, primary, "", portal, input)
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
	s = regtsError("SELECT $1::text::"+alias, code, primary, "", "", input)
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
func TestReferenceRegTSDiagnostics(t *testing.T) {
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, alias := range []string{"regconfig", "regdictionary"} {
			for _, tc := range []struct{ input, code, primary string }{
				{"08", "22P02", `invalid input syntax for type oid: "08"`},
				{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
				{"", "42602", "invalid name syntax"}, {"a..b", "42602", "invalid name syntax"}, {`"unclosed`, "42602", "invalid name syntax"},
				{"a.b.c.d", "42601", "improper qualified name (too many dotted names): a.b.c.d"},
				{"reg_ts_foreign.public.unaccent", "0A000", "cross-database references are not implemented: reg_ts_foreign.public.unaccent"},
			} {
				t.Run(alias+"/"+tc.input, func(t *testing.T) { regtsDiagnosticRoutes(t, p, b, c, alias, tc.input, tc.code, tc.primary) })
			}
			kind := strings.TrimPrefix(alias, "reg")
			if alias == "regconfig" {
				kind = "configuration"
			}
			for _, input := range []string{"reg_ts_missing", "reg_ts_missing.unaccent", " 123 ", "+123", "-1", "0x10"} {
				code, primary := "42704", "text search "+kind+" \""+strings.TrimSpace(input)+"\" does not exist"
				regtsDiagnosticRoutes(t, p, b, c, alias, input, code, primary)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, regtsError("SELECT (-1)::bigint::"+alias, "22003", "OID out of range", "", ""))
		}
		regtsSelect(t, p, b, c, "SELECT 'simple'::regdictionary AS d", []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"simple"}})
		regtsSelect(t, p, b, c, "SELECT 'simple'::regconfig AS c", []string{"c"}, []string{"REGCONFIG"}, [][]any{{"simple"}})
		// Genuine builtin configuration provider, without configuration DDL.
		regtsSelect(t, p, b, c, "SELECT oid::regconfig AS c,oid='simple'::regconfig::oid AS same FROM pg_catalog.pg_ts_config WHERE cfgnamespace='pg_catalog'::regnamespace AND cfgname='simple'", []string{"c", "same"}, []string{"REGCONFIG", "BOOL"}, [][]any{{"simple", true}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regtsError("SELECT 'é' AS prefix, 'a..b'::regdictionary", "42602", "invalid name syntax", "23", ""))
		regtsExec(t, p, b, c, "CREATE TABLE reg_ts_atomic(id integer,d regdictionary)", 0)
		q := "INSERT INTO reg_ts_atomic VALUES(1,'-'),(2,'reg_ts_missing')"
		d := fixture.ControlsReferenceDiagnostic("42704", `text search dictionary "reg_ts_missing" does not exist`)
		d.Position = fixture.Position(strings.Index(q, "'reg_ts_missing'") + 1)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
		regtsSelect(t, p, b, c, "SELECT id,d FROM reg_ts_atomic ORDER BY id", []string{"id", "d"}, []string{"INT4", "REGDICTIONARY"}, nil)
	})
}
