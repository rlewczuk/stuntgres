package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsBooleanByteInputAndHealthySession(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET application_name TO retained", 0))
			d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $3 = '...'"}
			s := fixture.ErrorDiagnostic("SELECT set_config($1,$2,$3)", d, "application_name", "changed", []byte("maybe"))
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "retained"))
			s = fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"healthy"}}, "application_name", "healthy", false)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, backend.PG, true) }
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
			s = fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}, "application_name", nil, nil)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, backend.PG, true) }
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
		}
	}
}
