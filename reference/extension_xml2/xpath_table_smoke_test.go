package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xt_control_reader", "xt_control_owner"})
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		q := xtQuery("id", "doc", "xt_src", "/doc/a|/doc/b", "id=1", "id integer,a integer,b integer") + " ORDER BY id,a"
		xtBoth(t, p, b, c, "zip", fixture.Select(q, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(7)}, {int64(1), int64(20), nil}}))
		for i := range 5 {
			args := []any{"id", "doc", "xt_absent", "[", "true"}
			args[i] = nil
			xtBoth(t, p, b, c, fmt.Sprintf("strict_%d", i), fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, nil, args...))
		}
		xtBoth(t, p, b, c, "no_matches", fixture.Select(xtQuery("id", "doc", "xt_src", "/missing", "id=1", "id integer,a text"), []string{"id", "a"}, []string{"INT4", "TEXT"}, nil))
		xtBoth(t, p, b, c, "malformed_key_retained", fixture.Select(xtQuery("id", "doc", "xt_src", "[", "id=5", "id integer,a text"), []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), nil}}))
		bad := fixture.ErrorDiagnostic(xtParameters, xml2Compile("Invalid expression"), "id", "doc", "xt_src", "[", "id=1")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		xml2Held(t, p, b, c, bad, fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "10"}, {int64(1), "20"}}, "id", "doc", "xt_src", "/doc/a", "id=1"))
		xtFailure(t, p, b, c, "evaluation_diagnostic_accumulation",
			xtQuery("id", "doc", "xt_src", "/p:doc|[", "id=3", "id integer,unresolved text,invalid text"),
			xml2Compile("Undefined namespace prefix\nInvalid expression"), false)
		xtFailure(t, p, b, c, "diagnostic_invocation_isolation",
			xtQuery("id", "doc", "xt_src", "[", "id=3", "id integer,a text"),
			xml2Compile("Invalid expression"), false)
		xtBoth(t, p, b, c, "healthy_after_accumulated_error",
			fixture.Select(q, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"},
				[][]any{{int64(1), int64(10), int64(7)}, {int64(1), int64(20), nil}}))
		xtExec(t, p, b, c, "CREATE TABLE xt_smoke_audit(id integer)")
		criteria := "false; INSERT INTO xt_smoke_audit VALUES (1); SELECT 9, '<doc><a>1</a></doc>'"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(xtQuery("id", "doc", "xt_src", "/doc/a", criteria, "id integer,a integer"), []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(1)}}))
		criteria = "false; INSERT INTO xt_smoke_audit VALUES (2); SELECT 9, '<doc><a>x</a></doc>'"
		xtFailure(t, p, b, c, "later_cast_rolls_back_write", xtQuery("id", "doc", "xt_src", "/doc/a", criteria, "id integer,a integer"), xml2Error("22P02", `invalid input syntax for type integer: "x"`), false)
		xml2Both(t, p, b, c, fixture.Select("SELECT id FROM xt_smoke_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		// No stored consumer was created by these SQL probes. Reinstallation
		// then exercises the complete typed-view/current-name control lifecycle.
		xtExec(t, p, b, c, "DROP EXTENSION xml2")
		xtControls(t, p, b, c)
	})
}
