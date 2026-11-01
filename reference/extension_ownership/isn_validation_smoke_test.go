package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationFeatureSmoke(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		// The first new-behavior native red must be real, not an import failure.
		d := isnValidationChecksum(0, isnValidationBad[0])
		d.Context = "unnamed portal parameter $1 = '...'"
		run(isnValidationQueryError("SELECT $1::ean13", d, isnValidationBad[0]))
		getter := fixture.IdentityHeld(t, p.Ctx, c, "SELECT isn_weak() AS v")
		defer getter.Statement.Close()
		for _, on := range []bool{true, false, true} {
			isnValidationMode(run, on)
			fixture.RunStep(t, p.Ctx, getter, b.Recorder, isnValidationBool(getter.SQL, on))
		}
		for i, role := range isnConversionRoles {
			isnConversionRoutes(t, p, b, c, isnValidationValueStep(b, i, "$1::"+role.name, isnValidationBad[i], true, false))
			isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, "SELECT is_valid(NULL::"+role.name+") AS valid,make_valid(NULL::"+role.name+") AS repaired", []string{"valid", "repaired"}, []string{"BOOL", strings.ToUpper(role.name)}, [][]any{{nil, nil}}, nil))
		}
		other, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer other.Close()
			defer isnValidationCleanup(t, other)
			r := isnConversionRun(t, p, b, other)
			r(isnValidationBool("SELECT isn_weak() AS v", false))
			r(isnValidationQueryError("SELECT $1::ean13", d, isnValidationBad[0]))
		}()
		isnValidationSetting(run, true)
		run(fixture.Exec("CREATE TABLE iv_smoke(id integer,v ean13)", 0))
		run(fixture.Exec("INSERT INTO iv_smoke VALUES(1,$1),(2,$1),(3,NULL)", 3, isnValidationBad[0]))
		run(fixture.Exec("CREATE VIEW iv_smoke_view AS SELECT id,is_valid(v) AS valid,make_valid(v)::text AS repaired FROM iv_smoke", 0))
		run(fixture.Exec("UPDATE iv_smoke SET v=make_valid(v) WHERE id=1", 1))
		isnValidationMode(run, false)
		run(isnConversionRuntimeError("INSERT INTO iv_smoke VALUES(4,$1::text::ean13),(5,$2::text::ean13)", isnValidationChecksum(0, isnValidationBad[0]), "2222222222222", isnValidationBad[0]))
		isnValidationSmokeView(t, p, b, c)
		run(fixture.Exec("CREATE TABLE iv_smoke_input(v text)", 0))
		run(fixture.Exec("INSERT INTO iv_smoke_input VALUES('2222222222221')", 1))
		isnConversionRoutes(t, p, b, c, isnValidationBool("SELECT CASE WHEN isn_weak(true) THEN is_valid(v::ean13) ELSE true END AS v FROM iv_smoke_input", false))
		isnValidationSetting(run, true)
		if !b.PG {
			isnValidationSmokeControls(t, p, b, c)
		}
	})
}

func isnValidationSmokeView(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	isnConversionRoutes(t, p, b, c, isnValidationSmokeStep())
}

func isnValidationSmokeStep() fixture.Step {
	return fixture.Select("SELECT id,valid,repaired FROM iv_smoke_view ORDER BY id", []string{"id", "valid", "repaired"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), true, "222-222222222-2"}, {int64(2), false, "222-222222222-2"}, {int64(3), nil, nil}})
}

func isnValidationSmokeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
	rows, err := c.QueryContext(p.Ctx, isnValidationSmokeStep().Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
		t.Fatal(err)
	}
	isnValidationSmokeView(t, p, b, c)
	isnValidationSetting(isnConversionRun(t, p, b, c), true)
	after, err := rows.ColumnTypes()
	if err != nil || !reflect.DeepEqual(after, metadata) {
		t.Fatal("feature smoke lost original metadata", err)
	}
	fixture.RangeConsumeHeld(t, rows, isnValidationSmokeStep())
	connector, err := stuntgres.NewDriver().OpenConnector("isn_validation_smoke_destination")
	if err != nil {
		t.Fatal(err)
	}
	db, recorder := fixture.RecordedNativePool(t, connector)
	control := sql.OpenDB(connector)
	defer control.Close()
	if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
		t.Fatal(err)
	}
	x, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	defer isnValidationCleanup(t, x)
	destination := fixture.Backend{DB: db, Recorder: recorder}
	isnValidationSmokeView(t, p, destination, x)
	isnConversionRun(t, p, destination, x)(isnValidationBool("SELECT isn_weak() AS v", false))
	isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, control))
	isnConversionReplay(t, p, saved, isnValidationSmokeView)
}
