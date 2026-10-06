//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceNativeDumpPostgresRestore(t *testing.T) {
	p := newReferencePair(t)
	a, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pg, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	// The PostgreSQL interpreter is authenticated as stuntgres, not the native
	// bootstrap owner postgres. Do not switch it before interpreting the dump:
	// the stream must establish every relation's owner itself.
	sequenceProbeExec(t, p.ctx, pg, "CREATE ROLE sequence_dump_owner")
	defer sequenceProbeExec(t, p.ctx, pg, "RESET SESSION AUTHORIZATION; DROP OWNED BY postgres CASCADE; DROP OWNED BY sequence_dump_owner CASCADE; DROP ROLE sequence_dump_owner")
	for _, q := range []string{
		"CREATE ROLE sequence_dump_owner",
		"CREATE TABLE target(v INT)",
		"CREATE SEQUENCE s AS SMALLINT START 4 INCREMENT 2 MINVALUE 2 MAXVALUE 20 CACHE 3 CYCLE OWNED BY target.v",
		"CREATE UNLOGGED SEQUENCE uncalled START 7",
		"COMMENT ON SEQUENCE s IS 'restored metadata'",
		"CREATE SCHEMA sequence_dump_owned AUTHORIZATION sequence_dump_owner",
		"SET SESSION AUTHORIZATION sequence_dump_owner",
		"CREATE TABLE sequence_dump_owned.z_target(v INT)",
		"CREATE SEQUENCE sequence_dump_owned.a_seq INCREMENT -2 MINVALUE -10 MAXVALUE -2 START -4 CACHE 3 CYCLE OWNED BY sequence_dump_owned.z_target.v",
		"SET SESSION AUTHORIZATION postgres",
	} {
		sequenceProbeExec(t, p.ctx, a, q)
	}
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 4)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('sequence_dump_owned.a_seq')", -4)
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	sequenceProbeExec(t, p.ctx, a, "CREATE TEMP SEQUENCE local_seq START 50 CACHE 7")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('local_seq')", 50)
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("noncanonical sequence dump")
	}
	if _, err = pg.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL rejected native sequence dump", err)
	}
	sequenceProbeExec(t, p.ctx, pg, "RESET SESSION AUTHORIZATION")
	for _, object := range []struct{ schema, name, owner string }{
		{"public", "target", "postgres"},
		{"public", "s", "postgres"},
		{"public", "uncalled", "postgres"},
		{"sequence_dump_owned", "z_target", "sequence_dump_owner"},
		{"sequence_dump_owned", "a_seq", "sequence_dump_owner"},
	} {
		var owner string
		if err = pg.QueryRowContext(p.ctx, "SELECT pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2", object.schema, object.name).Scan(&owner); err != nil || owner != object.owner {
			t.Fatalf("restored owner of %s.%s = %q, want %q: %v", object.schema, object.name, owner, object.owner, err)
		}
	}
	for _, db := range []*sql.Conn{a, pg} {
		for _, state := range []struct {
			name   string
			last   int64
			called bool
		}{{"s", 8, true}, {"uncalled", 7, false}, {"sequence_dump_owned.a_seq", -8, true}} {
			var last int64
			var called bool
			if err = db.QueryRowContext(p.ctx, "SELECT last_value,is_called FROM "+state.name).Scan(&last, &called); err != nil || last != state.last || called != state.called {
				t.Fatalf("restored %s state = (%d,%t), want (%d,%t): %v", state.name, last, called, state.last, state.called, err)
			}
		}
	}
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 10)
	sequenceProbeValue(t, p.ctx, pg, "SELECT nextval('s')", 10)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('uncalled')", 7)
	sequenceProbeValue(t, p.ctx, pg, "SELECT nextval('uncalled')", 7)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('local_seq')", 51)
	if err = stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE SEQUENCE replacement; SELECT setval('replacement',0,true);")); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 10)
	for _, db := range []*sql.Conn{a, pg} {
		sequenceProbeValue(t, p.ctx, db, "SELECT nextval('sequence_dump_owned.a_seq')", -10)
		for _, q := range []string{"DROP TABLE target RESTRICT", "DROP TABLE sequence_dump_owned.z_target RESTRICT"} {
			sequenceProbeExec(t, p.ctx, db, q)
		}
		for _, q := range []string{"SELECT * FROM s", "SELECT * FROM sequence_dump_owned.a_seq"} {
			rows, queryErr := db.QueryContext(p.ctx, q)
			if rows != nil {
				rows.Close()
			}
			referenceAssertDiagnostic(t, queryErr, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "` + strings.TrimPrefix(q, "SELECT * FROM ") + `" does not exist`, Position: "15"})
		}
		sequenceProbeValue(t, p.ctx, db, "SELECT nextval('uncalled')", 8)
	}
}
