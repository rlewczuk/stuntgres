package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSettingsBooleanByteInputAndHealthySession(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET application_name TO retained", 0))
			d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $3 = '...'"}
			s := referenceErrorDiagnostic("SELECT set_config($1,$2,$3)", d, "application_name", "changed", []byte("maybe"))
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, c, backend.recorder, s)
			referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "retained"))
			s = referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"healthy"}}, "application_name", "healthy", false)
			s.prepared = prepared
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, backend.pg, true) }
			referenceRunStep(t, p.ctx, c, backend.recorder, s)
			s = referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}, "application_name", nil, nil)
			s.prepared = prepared
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, backend.pg, true) }
			referenceRunStep(t, p.ctx, c, backend.recorder, s)
		}
	}
}
