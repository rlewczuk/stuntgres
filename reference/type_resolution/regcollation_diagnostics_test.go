package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type regObjectInvalid struct{ input, code, message string }

func regObjectError(query, code, message, position, portal string, args ...any) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position, want.Context = position, portal
	s := fixture.ErrorDiagnostic(query, want, args...)
	s.SelectRows = true
	return s
}

func regObjectDiagnostics(t *testing.T, kind, valid, missingCode, missingMessage string, facts []regObjectInvalid) {
	regObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		c, b.Recorder = noticeConn, recorder
		frameQuery := "SELECT $1::" + kind + " AS r"
		runtimeQuery := "SELECT $1::text::" + kind + " AS r"
		frame, err := c.PrepareContext(p.Ctx, frameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		runtime, err := c.PrepareContext(p.Ctx, runtimeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		for _, fact := range facts {
			t.Run(fact.input, func(t *testing.T) {
				literal := "SELECT " + regObjectLiteral(fact.input) + "::" + kind + " AS r"
				s := regObjectError(literal, fact.code, fact.message, "8", "")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				portal := "unnamed portal parameter $1 = '...'"
				if fact.input == "" {
					portal = "unnamed portal parameter $1 = ''"
				}
				s = regObjectError(frameQuery, fact.code, fact.message, "", portal, fact.input)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: frame, SQL: frameQuery}, b.Recorder, s)
				s = regObjectError(runtimeQuery, fact.code, fact.message, "", "", fact.input)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: runtime, SQL: runtimeQuery}, b.Recorder, s)
			})
		}
		for _, value := range []any{valid, nil} {
			var output any = valid
			if kind == "regcollation" {
				output = `"C"`
			}
			if value == nil {
				output = nil
			}
			for _, held := range []fixture.HeldStatement{{Executor: c, Statement: frame, SQL: frameQuery}, {Executor: c, Statement: runtime, SQL: runtimeQuery}} {
				regObjectRunStep(t, p, b, held, regObjectSelect(held.SQL, []string{"r"}, []string{strings.ToUpper(kind)}, [][]any{{output}}, value))
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError("SELECT 'é' AS prefix, 'ro_absent'::"+kind, missingCode, missingMessage, "23", ""))
		for _, query := range []string{"SELECT (-1)::bigint::" + kind, "SELECT 4294967296::bigint::" + kind} {
			regObjectBoth(t, p, b, c, regObjectError(query, "22003", "OID out of range", "", ""))
		}
		regObjectBoth(t, p, b, c, regObjectError("SELECT TRUE::"+kind, "42846", "cannot cast type boolean to "+kind, "12", ""))
		if len(notices) != 0 {
			t.Errorf("alias input emitted unexpected notices: %#v", notices)
		}
	})
}

func regObjectMalformed() []regObjectInvalid {
	var facts []regObjectInvalid
	for _, input := range []string{"", " \t ", ".s", "s.", "s..t", `"s`, `"s"tail`} {
		facts = append(facts, regObjectInvalid{input, "42602", "invalid name syntax"})
	}
	return append(facts,
		regObjectInvalid{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		regObjectInvalid{"08", "22P02", `invalid input syntax for type oid: "08"`})
}

func TestReferenceRegCollationDiagnostics(t *testing.T) {
	facts := regObjectMalformed()
	for _, pair := range [][2]string{{"ro_absent", "ro_absent"}, {"MiSsInG", "missing"}, {`"MiSsInG"`, "MiSsInG"}, {"absent_rc_schema.c", "absent_rc_schema.c"}, {`""`, ""}, {"-1", "-1"}, {" 10 ", "10"}, {"+10", "+10"}, {"0x10", "0x10"}} {
		facts = append(facts, regObjectInvalid{pair[0], "42704", `collation "` + pair[1] + `" for encoding "UTF8" does not exist`})
	}
	facts = append(facts,
		regObjectInvalid{"otherdb.public.c", "0A000", "cross-database references are not implemented: otherdb.public.c"},
		regObjectInvalid{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w"})
	regObjectDiagnostics(t, "regcollation", `pg_catalog."C"`, "42704", `collation "ro_absent" for encoding "UTF8" does not exist`, facts)
}

func TestReferenceRegNamespaceDiagnostics(t *testing.T) {
	facts := regObjectMalformed()
	for _, pair := range [][2]string{{"ro_absent", "ro_absent"}, {"MiSsInG", "missing"}, {`"MiSsInG"`, "MiSsInG"}, {`""`, ""}, {"-1", "-1"}, {" 10 ", "10"}, {"+10", "+10"}, {"0x10", "0x10"}, {"pg_temp", "pg_temp"}} {
		facts = append(facts, regObjectInvalid{pair[0], "3F000", `schema "` + pair[1] + `" does not exist`})
	}
	for _, input := range []string{"public.foo", "currentdb.public.foo", "x.y.z.w"} {
		facts = append(facts, regObjectInvalid{input, "42602", "invalid name syntax"})
	}
	regObjectDiagnostics(t, "regnamespace", "pg_catalog", "3F000", `schema "ro_absent" does not exist`, facts)
}
