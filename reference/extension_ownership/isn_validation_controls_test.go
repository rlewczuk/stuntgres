package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func isnValidationControlSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE iv_control(id integer NOT NULL,v ean13)",
		"CREATE VIEW iv_control_view AS SELECT id,is_valid(v) AS valid,make_valid(v)::text AS repaired,v::text AS original FROM iv_control",
		"CREATE TABLE iv_default(v ean13 DEFAULT make_valid('2222222222222!'::ean13),CONSTRAINT iv_only_valid CHECK(is_valid(v)))",
		"CREATE VIEW iv_mode_view AS SELECT isn_weak() AS v",
		"CREATE VIEW iv_setter_view AS SELECT isn_weak(isn_weak()) AS v",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO iv_control VALUES(1,'2222222222222!'),(2,'2222222222222'),(3,NULL),(4,'0000000000000!'),(5,'9999999999994!')", 5))
	run(fixture.Exec("INSERT INTO iv_default DEFAULT VALUES", 1))
	for i, role := range isnConversionRoles {
		run(fixture.Exec("CREATE TABLE iv_role_"+role.name+"(id integer,v "+role.name+")", 0))
		run(fixture.Exec("INSERT INTO iv_role_"+role.name+" VALUES(1,$1),(2,NULL)", 2, isnValidationBad[i]+"!"))
		run(fixture.Exec("CREATE VIEW iv_view_"+role.name+" AS SELECT id,is_valid(v) AS valid,make_valid(v)::text AS repaired FROM iv_role_"+role.name, 0))
	}
}

func isnValidationControlView() fixture.Step {
	return fixture.Select("SELECT id,valid,repaired,original FROM iv_control_view ORDER BY id", []string{"id", "valid", "repaired", "original"}, []string{"INT4", "BOOL", "TEXT", "TEXT"}, [][]any{
		{int64(1), false, "222-222222222-2", "222-222222222-2!"},
		{int64(2), true, "222-222222222-2", "222-222222222-2"},
		{int64(3), nil, nil, nil},
		{int64(4), false, "000-000000000-0", "000-000000000-0!"},
		{int64(5), false, "999-999999999-4", "999-999999999-4!"},
	})
}

func isnValidationControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := isnConversionRun(t, p, b, c)
	isnConversionRoutes(t, p, b, c, isnValidationControlView())
	isnConversionRoutes(t, p, b, c, fixture.Select("SELECT v::text AS v,is_valid(v) AS valid FROM iv_default", []string{"v", "valid"}, []string{"TEXT", "BOOL"}, [][]any{{"222-222222222-2", true}}))
	for i, role := range isnConversionRoles {
		s := fixture.Select("SELECT id,valid,repaired FROM iv_view_"+role.name+" ORDER BY id", []string{"id", "valid", "repaired"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), false, isnValidationOutput(i)}, {int64(2), nil, nil}})
		isnConversionRoutes(t, p, b, c, s)
	}
	for _, q := range []string{
		`SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='iv_control_view'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid IN ('is_valid(ean13)'::regprocedure,'make_valid(ean13)'::regprocedure) AND d.deptype='n'`,
		`SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='iv_setter_view'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid IN ('isn_weak()'::regprocedure,'isn_weak(boolean)'::regprocedure) AND d.deptype='n'`,
	} {
		run(fixture.OwnershipCount(q, 2))
	}
}

func isnValidationStoredMode(run func(fixture.Step), on bool) {
	for _, view := range []string{"iv_mode_view", "iv_setter_view"} {
		run(isnValidationBool("SELECT v FROM "+view, on))
	}
	isnValidationSetting(run, on)
}

func TestReferenceIsnValidationControls(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		isnValidationControlSetup(run)
		isnValidationMode(run, false)
		isnValidationControlCheck(t, p, b, c)
		isnValidationStoredMode(run, false)
		// Constraint failures compare the complete failing row and preserve the
		// already committed default row; explicit ! works with weak mode off.
		d := isnConversionDiagnostic("23514", `new row for relation "iv_default" violates check constraint "iv_only_valid"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (222-222222222-2!).", "public", "iv_default", "iv_only_valid"
		run(isnConversionRuntimeError("INSERT INTO iv_default VALUES('2222222222222!'::ean13)", d))
		run(fixture.OwnershipCount("SELECT count(*) FROM iv_default", 1))
		if b.PG {
			return // Reset/Dump/Restore have no server equivalent.
		}
		isnValidationMode(run, true)
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		held := fixture.IdentityHeld(t, p.Ctx, c, isnValidationControlView().Query)
		defer held.Statement.Close()
		typed := fixture.IdentityHeld(t, p.Ctx, c, "SELECT is_valid($1::ean13) AS v")
		defer typed.Statement.Close()
		fixture.RunStep(t, p.Ctx, typed, b.Recorder, isnValidationBool(typed.SQL, false, "2222222222222!"))
		old := make([]*sql.Rows, 2)
		metadata := make([][]*sql.ColumnType, 2)
		for i := range old {
			var err error
			old[i], err = c.QueryContext(p.Ctx, isnValidationControlView().Query)
			if err != nil {
				t.Fatal(err)
			}
			defer old[i].Close()
			metadata[i], err = old[i].ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		connector, err := stuntgres.NewDriver().OpenConnector("isn_validation_restore")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		control := sql.OpenDB(connector)
		defer control.Close()
		destination, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		defer isnValidationCleanup(t, destination)
		destBackend := fixture.Backend{DB: recorded, Recorder: recorder}
		destRun := isnConversionRun(t, p, destBackend, destination)
		destRun(fixture.Exec("CREATE TABLE iv_spacer(v integer)", 0))
		destRun(fixture.Exec("CREATE EXTENSION isn", 0))
		for _, sig := range []string{"is_valid(ean13)", "make_valid(upc)", "isn_weak()", "isn_weak(boolean)"} {
			q := "SELECT '" + sig + "'::regprocedure::bigint"
			if fixture.OwnershipID(t, p, c, q) == fixture.OwnershipID(t, p, destination, q) {
				t.Fatal("destination routine identity was not displaced", sig)
			}
		}
		for i, target := range []struct {
			control *sql.DB
			backend fixture.Backend
			conn *sql.Conn
			on bool
		}{{p.Native, b, c, true}, {control, destBackend, destination, false}} {
			if err := stuntgres.Reset(p.Ctx, target.control); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			isnValidationControlCheck(t, p, target.backend, target.conn)
			isnValidationStoredMode(isnConversionRun(t, p, target.backend, target.conn), target.on)
			isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, target.control))
			got, err := old[i].ColumnTypes()
			if err != nil || !reflect.DeepEqual(got, metadata[i]) {
				t.Fatal("replacement mutated retained descriptors", err)
			}
			fixture.RangeConsumeHeld(t, old[i], isnValidationControlView())
			isnValidationRestoreFailure(t, p, target.backend, target.conn, target.control, saved, target.on)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, isnValidationControlView())
		fixture.RunStep(t, p.Ctx, typed, b.Recorder, isnValidationQueryError(typed.SQL, isnConversionDiagnostic("0A000", "cached plan must not change parameter type"), "2222222222222!"))
		run(fixture.Prepared(isnValidationBool(typed.SQL, false, "2222222222222!")))
		isnConversionReplay(t, p, saved, func(t *testing.T, p *fixture.Pair, replay fixture.Backend, conn *sql.Conn) {
			defer isnValidationCleanup(t, conn)
			isnValidationControlCheck(t, p, replay, conn)
			// The same replay physical backend might already have loaded isn;
			// DISCARD ALL resets values, it does not unload the module.
			r := isnConversionRun(t, p, replay, conn)
			r(fixture.Exec("RESET isn.weak", 0))
			isnValidationStoredMode(r, false)
		})
	})
}

func isnValidationRestoreFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, control *sql.DB, saved string, on bool) {
	t.Helper()
	q := `SELECT oid::bigint,proargtypes::text,prorettype::bigint FROM pg_proc WHERE pronamespace='public'::regnamespace ORDER BY oid`
	before := fixture.OwnershipSnapshot(t, p, c, q)
	rows, err := c.QueryContext(p.Ctx, isnValidationControlView().Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	err = stuntgres.Restore(p.Ctx, control, strings.NewReader(saved+"\nINSERT INTO public.iv_default VALUES('2222222222222!'::public.ean13);\n"))
	d := isnConversionDiagnostic("23514", `new row for relation "iv_default" violates check constraint "iv_only_valid"`)
	d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (222-222222222-2!).", "public", "iv_default", "iv_only_valid"
	fixture.AssertDiagnostic(t, err, &d)
	fixture.OwnershipUnchanged(t, p, c, q, before)
	isnValidationControlCheck(t, p, b, c)
	isnValidationStoredMode(isnConversionRun(t, p, b, c), on)
	fixture.RangeConsumeHeld(t, rows, isnValidationControlView())
	isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, control))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, control, strings.NewReader(saved)); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled restore must preserve ordinary context error", err)
	}
	fixture.OwnershipUnchanged(t, p, c, q, before)
	isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, control))
}
