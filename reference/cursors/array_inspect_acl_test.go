package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayInspectACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ai_reader"})
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ai_reader", "CREATE TABLE ai_acl(a INT[],b INT[])", "CREATE SCHEMA ai_private", "CREATE TABLE ai_private.hidden(a INT[])"} {
			run(fixture.Exec(q, 0))
		}
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA ai_private CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
			if !b.PG {
				if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("INSERT INTO ai_acl VALUES ('{1,NULL}','{2}')", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ai_reader", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO ai_reader", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ai_reader", 0))
		calls := []struct {
			name, tail, typ string
			want            any
		}{
			{"array_dims", "", "TEXT", "[1:2]"}, {"array_length", ",1", "INT4", int64(2)}, {"array_lower", ",1", "INT4", int64(1)},
			{"array_upper", ",1", "INT4", int64(2)}, {"array_ndims", "", "INT4", int64(1)}, {"cardinality", "", "INT4", int64(2)},
		}
		denied := func(q string) {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ai_acl"))
			s.SelectRows, s.ErrorStage = true, "execute"
			arrayInspectBoth(t, p, b, c, s)
		}
		for _, call := range calls {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				denied("SELECT " + call.name + "(a" + call.tail + ") FROM ai_acl" + suffix)
			}
		}
		denied("SELECT coalesce(cardinality(a),0) FROM ai_acl WHERE FALSE")
		q := "SELECT array_dims(a) FROM ai_private.hidden"
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ai_private")
		d.Position = arrayInspectDiagnostic(q, "ai_private", "42501", d.Message, "").Position
		s := fixture.ErrorDiagnostic(q, d)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a) ON ai_acl TO ai_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ai_reader", 0))
		for _, call := range calls {
			arrayInspectBoth(t, p, b, c, fixture.Select("SELECT "+call.name+"(a"+call.tail+") AS v FROM ai_acl", []string{"v"}, []string{call.typ}, [][]any{{call.want}}))
			denied("SELECT " + call.name + "(b" + call.tail + ") FROM ai_acl")
		}
	})
}
