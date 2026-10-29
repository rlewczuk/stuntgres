package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6/contrib/spi, not native output.
func refintCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			clean := func() {
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP EXTENSION IF EXISTS refint CASCADE", `DROP SCHEMA IF EXISTS "Refint Q" CASCADE`} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(q, err) }
				}
			}
			clean()
			defer clean()
			notices = nil
			if install { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION refint", 0)) }
			body(t, p, b, c, r, &notices)
		})
	}
}

func refintError(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func refintNotices(t *testing.T, notices *[]fixture.Diagnostic, messages ...string) {
	t.Helper()
	want := make([]fixture.Diagnostic, len(messages))
	for i, m := range messages { want[i] = fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: m} }
	if len(*notices) != len(want) || len(want) > 0 && !reflect.DeepEqual(*notices, want) { t.Fatalf("notices got %#v want %#v", *notices, want) }
	*notices = nil
}

