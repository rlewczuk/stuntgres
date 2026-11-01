package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationLifecycle(t *testing.T) {
	for _, state := range []string{"installed", "detached", "origin_absent"} {
		t.Run(state, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"iv_reader"})
			isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				for _, q := range []string{"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE EXTENSION isn SCHEMA eo_old", "SET search_path=eo_old,pg_catalog", "CREATE ROLE iv_reader", "CREATE TABLE public.iv_identity(id integer,v ean13)", "CREATE VIEW public.iv_identity_view AS SELECT id,is_valid(v) AS valid,make_valid(v)::text AS repaired FROM public.iv_identity"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO public.iv_identity VALUES(1,'2222222222222!'),(2,NULL)", 2))
				view := fixture.Select("SELECT id,valid,repaired FROM public.iv_identity_view ORDER BY id", []string{"id", "valid", "repaired"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), false, "222-222222222-2"}, {int64(2), nil, nil}})
				held := fixture.IdentityHeld(t, p.Ctx, c, view.Query)
				defer held.Statement.Close()
				isnConversionRoutes(t, p, b, c, view)
				objects := []string{"TYPE eo_old.ean13", "FUNCTION eo_old.ean13_in(cstring)", "FUNCTION eo_old.ean13_out(eo_old.ean13)", "FUNCTION eo_old.is_valid(eo_old.ean13)", "FUNCTION eo_old.make_valid(eo_old.ean13)", "FUNCTION eo_old.isn_weak()", "FUNCTION eo_old.isn_weak(boolean)"}
				if state != "installed" {
					for _, object := range objects {
						run(fixture.Exec("ALTER EXTENSION isn DROP "+object, 0))
					}
				}
				if state == "origin_absent" {
					run(fixture.Exec("DROP EXTENSION isn", 0))
				}
				id := fixture.OwnershipID(t, p, c, "SELECT 'eo_old.is_valid(eo_old.ean13)'::regprocedure::bigint")
				for _, q := range []string{"ALTER FUNCTION eo_old.is_valid(eo_old.ean13) RENAME TO marked_valid", "ALTER FUNCTION eo_old.marked_valid(eo_old.ean13) SET SCHEMA eo_new", "GRANT USAGE ON SCHEMA eo_old,eo_new TO iv_reader", "REVOKE EXECUTE ON FUNCTION eo_new.marked_valid(eo_old.ean13) FROM PUBLIC"} {
					run(fixture.Exec(q, 0))
				}
				if fixture.OwnershipID(t, p, c, "SELECT 'eo_new.marked_valid(eo_old.ean13)'::regprocedure::bigint") != id {
					t.Fatal("rename/move lost genuine validation identity")
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, view)
				q := "SELECT eo_new.marked_valid($1::eo_old.ean13) AS v"
				call := fixture.IdentityHeld(t, p.Ctx, c, q)
				defer call.Statement.Close()
				fixture.RunStep(t, p.Ctx, call, b.Recorder, isnValidationBool(q, false, "2222222222222!"))
				run(fixture.Exec("SET ROLE iv_reader", 0))
				denied := isnValidationQueryError(q, isnConversionDiagnostic("42501", "permission denied for function marked_valid"), "2222222222222!")
				fixture.RunStep(t, p.Ctx, call, b.Recorder, denied)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION eo_new.marked_valid(eo_old.ean13) TO iv_reader", 0))
				run(fixture.Exec("SET ROLE iv_reader", 0))
				fixture.RunStep(t, p.Ctx, call, b.Recorder, isnValidationBool(q, false, "2222222222222!"))
				run(fixture.Exec("RESET ROLE", 0))
				isnConversionRoutes(t, p, b, c, view)
				isnValidationMode(run, true)
				isnConversionRoutes(t, p, b, c, isnValidationBool("SELECT eo_new.marked_valid($1::text::eo_old.ean13) AS v", false, isnValidationBad[0]))
				isnValidationMode(run, false)
				isnValidationSurvivorControls(t, p, b, c, state, view)
			})
		})
	}
	t.Run("recreate_fences", func(t *testing.T) {
		isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			typed := fixture.IdentityHeld(t, p.Ctx, c, "SELECT is_valid($1::ean13) AS v")
			defer typed.Statement.Close()
			stable := fixture.IdentityHeld(t, p.Ctx, c, "SELECT is_valid($1::text::ean13) AS v")
			defer stable.Statement.Close()
			getter := fixture.IdentityHeld(t, p.Ctx, c, "SELECT isn_weak() AS v")
			defer getter.Statement.Close()
			for _, h := range []fixture.HeldStatement{typed, stable} {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, isnValidationBool(h.SQL, false, "2222222222222!"))
			}
			isnValidationMode(run, true)
			run(fixture.Exec("DROP EXTENSION isn", 0))
			isnValidationSetting(run, true)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			fixture.RunStep(t, p.Ctx, getter, b.Recorder, isnValidationBool(getter.SQL, true))
			fixture.RunStep(t, p.Ctx, stable, b.Recorder, isnValidationBool(stable.SQL, false, isnValidationBad[0]))
			if !b.PG {
				fixture.RunStep(t, p.Ctx, typed, b.Recorder, isnValidationQueryError(typed.SQL, isnConversionDiagnostic("0A000", "cached plan must not change parameter type"), "2222222222222!"))
			}
			run(fixture.Prepared(isnValidationBool(typed.SQL, false, "2222222222222!")))
		})
	})
}

func isnValidationSurvivorControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, state string, view fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
	signatures := []string{"eo_new.marked_valid(eo_old.ean13)", "eo_old.make_valid(eo_old.ean13)", "eo_old.isn_weak()", "eo_old.isn_weak(boolean)"}
	declarations := make([][][]any, len(signatures))
	for i, signature := range signatures {
		declarations[i] = fixture.OwnershipSnapshot(t, p, c, fixture.OwnershipSurvivorDeclaration(signature))
	}
	connector, err := stuntgres.NewDriver().OpenConnector("isn_validation_survivor_" + state)
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	control := sql.OpenDB(connector)
	defer control.Close()
	for _, q := range []string{"CREATE ROLE iv_reader", "CREATE TABLE iv_spacer(v integer)"} {
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec(q, 0))
	}
	for _, target := range []struct {
		control, recorded *sql.DB
		recorder *fixture.Recorder
	}{{p.Native, b.DB, b.Recorder}, {control, recorded, recorder}} {
		if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		x, err := target.recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer x.Close()
			defer isnValidationCleanup(t, x)
			backend := fixture.Backend{DB: target.recorded, Recorder: target.recorder}
			run := isnConversionRun(t, p, backend, x)
			run(fixture.Exec("SET search_path=eo_old,pg_catalog", 0))
			isnConversionRoutes(t, p, backend, x, view)
			isnConversionRoutes(t, p, backend, x, isnValidationBool("SELECT eo_new.marked_valid($1::text::eo_old.ean13) AS v", false, "2222222222222!"))
			isnValidationMode(run, true)
			isnConversionRoutes(t, p, backend, x, isnValidationBool("SELECT eo_new.marked_valid($1::text::eo_old.ean13) AS v", false, isnValidationBad[0]))
			isnValidationMode(run, false)
			for i, signature := range signatures {
				fixture.OwnershipUnchanged(t, p, x, fixture.OwnershipSurvivorDeclaration(signature), declarations[i])
			}
			run(fixture.Exec("SET ROLE iv_reader", 0))
			isnConversionRoutes(t, p, backend, x, isnValidationBool("SELECT eo_new.marked_valid($1::text::eo_old.ean13) AS v", false, "2222222222222!"))
			run(fixture.Exec("RESET ROLE", 0))
			isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, target.control))
		}()
	}
}
