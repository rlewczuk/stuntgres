package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationSettings(t *testing.T) {
	t.Run("assignment_and_journal", func(t *testing.T) {
		isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			isnValidationMode(run, false)
			for _, on := range []bool{false, true} {
				isnValidationMode(run, on)
				isnConversionRoutes(t, p, b, c, isnValidationBool("SELECT isn_weak(NULL::boolean) AS v", nil))
				isnValidationSetting(run, on)
				isnConversionRoutes(t, p, b, c, isnValidationBool("SELECT isn_weak() AS v", on))
			}
			for _, alias := range []string{"true", "yes", "1", "on"} {
				run(fixture.Exec("SET isn.weak="+isnConversionLiteral(alias), 0))
				isnValidationSetting(run, true)
			}
			run(fixture.Select("SELECT current_setting('isn.weak') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"on"}}))
			run(fixture.Select("SELECT set_config('isn.weak','false',false) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"off"}}))
			isnValidationSetting(run, false)
			for _, end := range []string{"COMMIT", "ROLLBACK"} {
				isnValidationMode(run, false)
				run(fixture.Exec("BEGIN", 0))
				isnValidationMode(run, true)
				run(fixture.Exec(end, 0))
				isnValidationSetting(run, end == "COMMIT")
				isnValidationMode(run, false)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET LOCAL isn.weak='on'", 0))
				isnValidationSetting(run, true)
				run(fixture.Exec(end, 0))
				isnValidationSetting(run, false)
			}
			run(fixture.Exec("BEGIN", 0))
			isnValidationMode(run, true)
			run(fixture.Exec("SET LOCAL isn.weak='off'", 0))
			isnValidationSetting(run, false)
			run(fixture.Exec("COMMIT", 0))
			isnValidationSetting(run, true)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT iv_outer", 0))
			isnValidationMode(run, false)
			run(fixture.Exec("SAVEPOINT iv_inner", 0))
			isnValidationMode(run, true)
			run(fixture.Exec("ROLLBACK TO iv_inner", 0))
			isnValidationSetting(run, false)
			run(fixture.Exec("RELEASE iv_inner", 0))
			run(fixture.Exec("ROLLBACK TO iv_outer", 0))
			isnValidationSetting(run, true)
			run(fixture.Exec("RELEASE iv_outer", 0))
			run(fixture.Exec("COMMIT", 0))
			isnValidationSetting(run, true)
			for _, reset := range []string{"RESET isn.weak", "RESET ALL", "DISCARD ALL"} {
				isnValidationMode(run, true)
				run(fixture.Exec(reset, 0))
				isnValidationSetting(run, false)
			}
		})
	})
	t.Run("routine_save_frames", func(t *testing.T) {
		isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			isnValidationMode(run, true)
			for _, q := range []string{
				"CREATE FUNCTION iv_local() RETURNS boolean LANGUAGE SQL SET isn.weak='off' AS $$SELECT set_config('isn.weak','on',true)::boolean$$",
				"CREATE FUNCTION iv_session() RETURNS boolean LANGUAGE SQL SET isn.weak='off' AS $$SELECT isn_weak(true)$$",
				"CREATE FUNCTION iv_null() RETURNS boolean LANGUAGE SQL SET isn.weak='off' AS $$SELECT isn_weak(NULL::boolean)$$",
				"CREATE FUNCTION iv_nested() RETURNS boolean LANGUAGE SQL SET isn.weak='off' AS $$SELECT iv_local()$$",
				"CREATE FUNCTION iv_caught() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM isn_weak(true); RAISE EXCEPTION 'undo'; EXCEPTION WHEN raise_exception THEN PERFORM isn_weak(); END; RETURN isn_weak(); END$$",
			} {
				run(fixture.Exec(q, 0))
			}
			run(isnValidationBool("SELECT iv_local() AS v", true))
			isnValidationSetting(run, true)
			run(isnValidationBool("SELECT iv_null() AS v", nil))
			isnValidationSetting(run, true)
			isnValidationMode(run, false)
			run(isnValidationBool("SELECT iv_nested() AS v", true))
			isnValidationSetting(run, false)
			run(isnValidationBool("SELECT iv_caught() AS v", false))
			isnValidationSetting(run, false)
			run(isnValidationBool("SELECT iv_session() AS v", true))
			isnValidationSetting(run, true)
			isnValidationMode(run, false)
			run(fixture.Exec("BEGIN", 0))
			run(isnValidationBool("SELECT iv_session() AS v", true))
			run(fixture.Exec("ROLLBACK", 0))
			isnValidationSetting(run, false)
		})
	})
}

func TestReferenceIsnValidationEvaluation(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		isnValidationMode(run, false)
		getter := fixture.IdentityHeld(t, p.Ctx, c, "SELECT isn_weak() AS v")
		defer getter.Statement.Close()
		for _, on := range []bool{false, true, false} {
			isnValidationMode(run, on)
			fixture.RunStep(t, p.Ctx, getter, b.Recorder, isnValidationBool(getter.SQL, on))
		}
		run(fixture.Exec("CREATE TABLE iv_input(v text)", 0))
		run(fixture.Exec("INSERT INTO iv_input VALUES('2222222222221')", 1))
		onQuery := "SELECT CASE WHEN isn_weak(true) THEN is_valid(v::ean13) ELSE true END AS v FROM iv_input"
		offQuery := "SELECT CASE WHEN isn_weak(false) THEN true ELSE is_valid(v::ean13) END AS v FROM iv_input"
		heldOn := fixture.IdentityHeld(t, p.Ctx, c, onQuery)
		defer heldOn.Statement.Close()
		heldOff := fixture.IdentityHeld(t, p.Ctx, c, offQuery)
		defer heldOff.Statement.Close()
		for _, route := range []string{"direct", "prepared", "held"} {
			isnValidationMode(run, false)
			on := isnValidationBool(onQuery, false)
			off := isnValidationQueryError(offQuery, isnValidationChecksum(0, isnValidationBad[0]))
			switch route {
			case "direct":
				run(on)
				isnValidationSetting(run, true)
				run(off)
			case "prepared":
				run(fixture.Prepared(on))
				isnValidationSetting(run, true)
				run(fixture.Prepared(off))
			case "held":
				fixture.RunStep(t, p.Ctx, heldOn, b.Recorder, on)
				isnValidationSetting(run, true)
				fixture.RunStep(t, p.Ctx, heldOff, b.Recorder, off)
			}
			isnValidationSetting(run, true)
		}
		// Input admission, unlike the reached runtime cast above, precedes
		// projection. No assumption about sibling target-list order is needed.
		isnValidationMode(run, false)
		literal := "SELECT isn_weak(true),'2222222222221'::ean13"
		d := isnValidationChecksum(0, isnValidationBad[0])
		d.Position = fixture.Position(strings.Index(literal, "'") + 1)
		run(isnValidationQueryError(literal, d))
		s := fixture.Prepared(isnValidationQueryError(literal, d))
		s.ErrorStage = "prepare"
		run(s)
		isnValidationSetting(run, false)
		q := "SELECT isn_weak(true),$1::ean13"
		d = isnValidationChecksum(0, isnValidationBad[0])
		d.Context = "unnamed portal parameter $1 = '...'"
		isnConversionRoutes(t, p, b, c, isnValidationQueryError(q, d, isnValidationBad[0]))
		isnValidationSetting(run, false)
		for _, expression := range []string{"$1::ean13", "$1::text::ean13"} {
			q = "SELECT is_valid(" + expression + ") AS v"
			held := fixture.IdentityHeld(t, p.Ctx, c, q)
			isnValidationMode(run, true)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, isnValidationBool(q, false, isnValidationBad[0]))
			isnValidationMode(run, false)
			d = isnValidationChecksum(0, isnValidationBad[0])
			if expression == "$1::ean13" {
				d.Context = "unnamed portal parameter $1 = '...'"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, isnValidationQueryError(q, d, isnValidationBad[0]))
			held.Statement.Close()
		}
		isnValidationMode(run, true)
		constant := fixture.IdentityHeld(t, p.Ctx, c, "SELECT is_valid('2222222222221'::ean13) AS v")
		defer constant.Statement.Close()
		// Prepare admits the literal in weak mode; subsequent toggles cannot
		// repair its retained invalid bit.
		fixture.RunStep(t, p.Ctx, constant, b.Recorder, isnValidationBool(constant.SQL, false))
		isnValidationMode(run, false)
		fixture.RunStep(t, p.Ctx, constant, b.Recorder, isnValidationBool(constant.SQL, false))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, query := range []string{"SELECT isn_weak(true)", "INSERT INTO iv_input VALUES('2222222222222')"} {
				if _, err := c.ExecContext(ctx, query); !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled admission lost ordinary context error", err)
				}
			}
			isnValidationSetting(run, false)
			run(fixture.OwnershipCount("SELECT count(*) FROM iv_input", 1))
		}
	})
}
