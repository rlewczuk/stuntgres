package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *fdwSQLReader, run func(fixture.Step)) {
		pgUserMappingsSetup(t, c, run)
		pgUserMappingsSeed(run, true)
		held := pgUserMappingsHold(t, p, c)
		rows := pgUserMappingsRows(t, p, c, pgUserMappingsSpecs(true))
		pgUserMappingsMatrix(t, p, b, c, run, rows, &held)
		run(fixture.Exec(`ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(ADD special 'comma,{brace} apostrophe'' double" slash\ é')`, 0))
		const options = `{password=alice-secret,empty=,tag=é=🙂,"special=comma,{brace} apostrophe' double\" slash\\ é"}`
		specs := pgUserMappingsSpecs(true)
		specs[0].options = options
		rows = pgUserMappingsRows(t, p, c, specs)
		pgUserMappingsRoutes(t, p, b, c, &held, rows, "fdw_server")
		pgUserMappingBoth(run, fixture.Select("SELECT umoptions[4] AS special FROM pg_user_mappings WHERE srvname='fdw_server' AND usename='umv_alice'", []string{"special"}, []string{"TEXT"}, [][]any{{`special=comma,{brace} apostrophe' double" slash\ é`}}))
		if b.PG {
			return
		} // Dump/Reset/Restore are explicit native controls.
		run(fixture.Exec("CREATE TABLE umv_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO umv_sentinel VALUES(7)", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := held.QueryContext(p.Ctx, pgUserMappingsQuery, "fdw_server")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgUserMappingsRoutes(t, p, b, c, &held, nil, "fdw_server")
		pgUserMappingsConsume(t, b, old, rows)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		// Restored allocator values are independently resolved against the actual
		// destination's protected catalog, server and roles, never old OID numbers.
		restored := pgUserMappingsRows(t, p, c, specs)
		pgUserMappingsMatrix(t, p, b, c, run, restored, &held)
		connector, err := stuntgres.NewDriver().OpenConnector("pg_user_mappings independent destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		for _, role := range pgUserMappingsRoles {
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		target, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		targetBackend := b
		targetBackend.DB, targetBackend.Recorder = recorded, recorder
		targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, recorder, s) }
		targetHeld := pgUserMappingsHold(t, p, target)
		targetRows := pgUserMappingsRows(t, p, target, specs)
		pgUserMappingsMatrix(t, p, targetBackend, target, targetRun, targetRows, &targetHeld)
		// Mutating the independent destination cannot affect source metadata.
		targetRun(fixture.Exec("ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(SET password 'destination-only')", 0))
		pgUserMappingsRoutes(t, p, b, c, &held, restored, "fdw_server")
		prefix := dump.String() + "\nCREATE TABLE umv_staged(v integer); INSERT INTO umv_staged VALUES("
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(t, err, &diagnostic)
		unchanged := func() {
			pgUserMappingsMatrix(t, p, b, c, run, restored, &held)
			run(fixture.Select("SELECT v FROM umv_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='umv_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation acquired SQLSTATE", state.SQLState())
		}
		unchanged()
	})
}
