package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ginIDs(query string, ids []int64, args ...any) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select(query, []string{"id"}, []string{"INT4"}, rows, args...)
}

func ginUtilityError(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, query string, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, want)
	s.ErrorStage = "execute"
	classRoutes(t, p, b, conn, s)
	held := classHeld(t, p, conn, query)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

// Independently derived from REL_18_6 reloptions.c, not native diagnostics.
var ginInvalidOptions = []struct {
	options, message, detail string
}{
	{"fillfactor=80", `unrecognized parameter "fillfactor"`, ""},
	{"unknown=1", `unrecognized parameter "unknown"`, ""},
	{"fastupdate=wrong", `invalid value for boolean option "fastupdate": wrong`, ""},
	{"gin_pending_list_limit=bad", `invalid value for integer option "gin_pending_list_limit": bad`, ""},
	{"gin_pending_list_limit=63", `value 63 out of bounds for option "gin_pending_list_limit"`, `Valid values are between "64" and "2147483647".`},
	{"gin_pending_list_limit=2147483648", `invalid value for integer option "gin_pending_list_limit": 2147483648`, ""},
	{"fastupdate=on,fastupdate=off", `parameter "fastupdate" specified more than once`, ""},
}
