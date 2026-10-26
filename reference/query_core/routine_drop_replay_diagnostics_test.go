package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Ordinary enabled CREATE expectations remain distinct from reached invocation:
// parse failures name enclosing CREATE coordinates; native producer-free CREATE
// binding retains body coordinates and a non-runtime function Context.
func routineDropReplayBodyDiagnostic(body, createSQL string) fixture.Diagnostic {
	var d fixture.Diagnostic
	switch body {
	case "SELECT rd_missing(1), $1", "SELECT rd_missing(1), $2":
		slot := "$1"
		if strings.HasSuffix(body, "$2") {
			slot = "$2"
		}
		d = fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter "+slot)
		d.Position = fixture.Position(strings.Index(createSQL, slot) + 1)
	case "SELECT 1 +":
		d = fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(strings.LastIndex(createSQL, "$$") + 1)
	case "SELECT 'x'::text":
		d = fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
		d.Detail = "Actual return type is text."
		d.Context = "SQL function \"rd_bad_replay\""
	case "SELECT x FROM rd_missing_table":
		d = fixture.ControlsReferenceDiagnostic("42P01", "relation \"rd_missing_table\" does not exist")
		d.Position = "15"
		d.Context = "SQL function \"rd_bad_replay\""
	case "SELECT 1 OPERATOR(public.###) 2":
		d = fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: integer public.### integer")
		d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "10"
		d.Context = "SQL function \"rd_bad_replay\""
	default:
		panic("unspecified replay body diagnostic: " + body)
	}
	return d
}

func routineDropReplayLookupDiagnostic(denied bool) fixture.Diagnostic {
	if denied {
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema rd_private_source")
		d.Position = "8"
		d.Context = "SQL function \"rd_denied\""
		return d
	}
	d := fixture.ControlsReferenceDiagnostic("42725", "function public.rd_amb(unknown) is not unique")
	d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	d.Position = "8"
	d.Context = "SQL function \"rd_amb_caller\""
	return d
}

func TestReferenceRoutineDropReplayDiagnostics(t *testing.T) {
	for _, body := range []string{"SELECT $1", "SELECT $2", "SELECT 1 +", "SELECT 'x'::text", "SELECT x FROM rd_missing_table", "SELECT 1 OPERATOR(public.###) 2"} {
		t.Run(body, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					q := "CREATE FUNCTION public.rd_bad_replay() RETURNS integer LANGUAGE SQL AS $$" + body + "$$"
					// The SQL witness has no missing call ahead of its formal, so
					// PostgreSQL independently reaches the same formal boundary.
					contractBody := body
					if body == "SELECT $1" {
						contractBody = "SELECT rd_missing(1), $1"
					}
					if body == "SELECT $2" {
						contractBody = "SELECT rd_missing(1), $2"
					}
					native := routineDropReplayBodyDiagnostic(contractBody, q)
					pg := native
					if body == "SELECT x FROM rd_missing_table" {
						pg.Position = fixture.Position(strings.Index(q, "rd_missing_table") + 1)
						pg.Context = ""
					}
					if body == "SELECT 1 OPERATOR(public.###) 2" {
						pg.Position = fixture.Position(strings.Index(q, "OPERATOR") + 1)
						pg.Context = ""
					}
					s := fixture.ErrorDiagnostic(q, native)
					s.PostgresDiagnostic = &pg
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				})
			}
		})
	}
	t.Run("lookup permission and ambiguity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rd_replay_denied"})
		fixture.SchemaReferenceCleanup(t, "rd_private_source", "rd_private_schema")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				routineExec(t, p, b, c, "CREATE ROLE rd_replay_denied", "CREATE SCHEMA rd_private_source", "CREATE SCHEMA rd_private_schema AUTHORIZATION rd_replay_denied", "SET SESSION AUTHORIZATION rd_replay_denied")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "CREATE FUNCTION rd_private_schema.rd_denied() RETURNS integer LANGUAGE SQL AS $$SELECT rd_private_source.rd_missing(1)$$"
				native := routineDropReplayLookupDiagnostic(true)
				pg := native
				pg.Position = fixture.Position(strings.Index(q, "rd_private_source.rd_missing") + 1)
				pg.Context = ""
				s := fixture.ErrorDiagnostic(q, native)
				s.PostgresDiagnostic = &pg
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "CREATE FUNCTION public.rd_amb(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "CREATE FUNCTION public.rd_amb(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$")
				q = "CREATE FUNCTION public.rd_amb_caller() RETURNS integer LANGUAGE SQL AS $$SELECT public.rd_amb(NULL)$$"
				native = routineDropReplayLookupDiagnostic(false)
				pg = native
				pg.Position = fixture.Position(strings.Index(q, "public.rd_amb(NULL)") + 1)
				pg.Context = ""
				s = fixture.ErrorDiagnostic(q, native)
				s.PostgresDiagnostic = &pg
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}
