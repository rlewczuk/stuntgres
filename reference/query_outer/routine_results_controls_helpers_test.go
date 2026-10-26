package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const resultsNoMatchHint = "No function matches the given name and argument types. You might need to add explicit type casts."

// Local adapters retain the shared fixture's setup/recording/control contracts;
// they do not import another external test package or own a second fixture.
func resultsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func resultsNativeReplayRound(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend)) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(c, b)
	return dump.Bytes()
}
