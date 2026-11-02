package reference_test

import (
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func passwordCheckRegistrationError(t *testing.T, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if err == nil || errors.As(err, &state) { t.Fatal("registration must return an ordinary host error", err) }
}

func TestReferencePasswordCheckRegistration(t *testing.T) {
	names := []string{"pc_registration_db"}
	fixture.RoleReferenceCleanup(t, []string{"pc_registration_short", "pc_registration_valid"})
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenarioWithDriverConfig(t, (*stuntgres.Driver).RegisterPasswordCheck)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			for _, prepared := range []bool{false, true} {
				run := passwordCheckRun(t, p, c, b.Recorder, prepared)
				run(passwordCheckShow("8B"))
				run(passwordCheckShort("CREATE ROLE pc_registration_short PASSWORD 'x'", 8))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DATABASE pc_registration_db", 0))
			selected, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, names[0], "")
			for _, prepared := range []bool{false, true} {
				run := passwordCheckRun(t, p, selected, recorder, prepared)
				run(passwordCheckShow("8B"))
				run(passwordCheckShort("CREATE ROLE pc_registration_short PASSWORD 'x'", 8))
			}
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer peer.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET passwordcheck.min_password_length=12", 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, passwordCheckShow("8B"))
			if b.PG { return } // Go registration is the native activation boundary.
			d := p.NativeConnector.Driver().(*stuntgres.Driver)
			passwordCheckRegistrationError(t, d.RegisterPasswordCheck())
			other, err := d.OpenConnector("passwordcheck-registration-other-cluster")
			if err != nil { t.Fatal(err) }
			otherDB, otherRecorder := fixture.RecordedNativePool(t, other)
			fixture.RunStep(t, p.Ctx, otherDB, otherRecorder, passwordCheckShow("8B"))
			fixture.RunStep(t, p.Ctx, otherDB, otherRecorder, passwordCheckShort("CREATE ROLE pc_registration_short PASSWORD 'x'", 8))
		})
	}
	// No mutable global option, DSN flag or dump directive can activate a peer.
	for _, configured := range []bool{false, true} {
		var d stuntgres.Driver
		if configured {
			if err := d.RegisterPasswordCheck(); err != nil { t.Fatal("zero Driver", err) }
			passwordCheckRegistrationError(t, d.RegisterPasswordCheck())
		}
		c, err := d.OpenConnector("passwordcheck-registration-independent")
		if err != nil { t.Fatal(err) }
		passwordCheckRegistrationError(t, d.RegisterPasswordCheck()) // freeze before Ping
		db, recorder := fixture.RecordedNativePool(t, c)
		if configured {
			fixture.RunStep(t, p.Ctx, db, recorder, passwordCheckShow("8B"))
			fixture.RunStep(t, p.Ctx, db, recorder, passwordCheckShort("CREATE ROLE pc_registration_short PASSWORD 'x'", 8))
		} else {
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.ImplicitError("SHOW passwordcheck.min_password_length", fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "passwordcheck.min_password_length"`}))
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("CREATE ROLE pc_registration_valid PASSWORD 'x'", 0))
		}
		if err := db.Close(); err != nil { t.Fatal(err) }
	}
}

