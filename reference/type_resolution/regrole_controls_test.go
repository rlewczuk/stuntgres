package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const regroleUnknownRestoreSQL = "CREATE TABLE rr_unknown(r regrole); INSERT INTO rr_unknown VALUES('rr_absent');"

func regroleUnknownRestoreDiagnostic() fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42704", `role "rr_absent" does not exist`)
	want.Position = "67"
	return want
}

func regroleMissingDumpDiagnostic(t *testing.T, dump []byte) fixture.Diagnostic {
	t.Helper()
	// The runtime rr_data default does not resolve input at CREATE. The first
	// absent-role lookup is the opaque array DEFAULT in rr_recursive, before
	// any row INSERT. Position is a character offset in the full SQL stream,
	// not the statement-local offset or a value learned from native errors.
	table := bytes.Index(dump, []byte(`CREATE TABLE "public"."rr_recursive" (`))
	if table < 0 {
		t.Fatal("missing recursive table in actual dump")
	}
	prefix := []byte(`"a" REGROLE[] DEFAULT CAST(`)
	member := bytes.Index(dump[table:], prefix)
	if member < 0 {
		t.Fatal("missing first recursive array default in actual dump")
	}
	position := table + member + len(prefix)
	if !bytes.HasPrefix(dump[position:], []byte("'{rr_role}'")) {
		t.Fatal("first recursive default is not the expected role literal")
	}
	want := fixture.ControlsReferenceDiagnostic("42704", `role "rr_role" does not exist`)
	want.Position = fixture.Position(utf8.RuneCount(dump[:position]) + 1)
	return want
}

func TestReferenceRegroleAtomicityCancellation(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE rr_atomic(id int,r regrole)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_atomic VALUES(0,'rr_role')", 1))
		q := "INSERT INTO rr_atomic VALUES(1,'rr_role'),(2,'rr_absent')"
		result, err := c.ExecContext(p.Ctx, q)
		if err == nil {
			t.Fatal("invalid role insert succeeded")
		}
		if result != nil {
			t.Error("failed INSERT published result")
		}
		want := fixture.ControlsReferenceDiagnostic("42704", `role "rr_absent" does not exist`)
		want.Position = "46"
		fixture.AssertDiagnostic(t, err, &want)
		regroleBoth(t, p, b, c, fixture.Select("SELECT id,r FROM rr_atomic ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGROLE"}, [][]any{{int64(0), "rr_role"}}))
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(canceled, "SELECT r FROM rr_atomic")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled Query rows=%v err=%v", rows, err)
			}
			result, err := c.ExecContext(canceled, "INSERT INTO rr_atomic VALUES(3,'rr_role')")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled Exec result=%v err=%v", result, err)
			}
			cursorCtx, stop := context.WithCancel(p.Ctx)
			cursor, err := c.QueryContext(cursorCtx, "SELECT r FROM rr_atomic")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			stop()
			if cursor.Next() || !errors.Is(cursor.Err(), context.Canceled) {
				cursor.Close()
				t.Fatalf("canceled iteration=%v", cursor.Err())
			}
			if err := cursor.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceRegroleNativeControls(t *testing.T) {
	var nativeDump []byte
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE rr_data(id int,r regrole DEFAULT 'rr_role'::text::regrole)",
			"CREATE VIEW rr_null_view AS SELECT NULL::regrole AS r",
			"CREATE VIEW rr_data_view AS SELECT id,r FROM rr_data",
			"CREATE DOMAIN rr_control_array AS regrole[]",
			"CREATE TABLE rr_recursive(a regrole[] DEFAULT '{rr_role}'::regrole[],d rr_control_array DEFAULT '{rr_role}'::rr_control_array)",
			"CREATE TABLE rr_numeric(r regrole DEFAULT 3373::oid::regrole)",
			"CREATE VIEW rr_opaque_view AS SELECT '{rr_role}'::regrole[] AS a",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO rr_data VALUES(1,'rr_role'),(2,'"Mixed.Role"'),(3,NULL),(4,'0'),(5,'4294967295')`, 5))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_data(id) VALUES(6)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_recursive DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_numeric DEFAULT VALUES", 1))
		want := [][]any{{int64(1), "rr_role"}, {int64(2), `"Mixed.Role"`}, {int64(3), nil}, {int64(4), "-"}, {int64(5), "4294967295"}, {int64(6), "rr_role"}}
		for _, table := range []string{"rr_data", "rr_data_view"} {
			regroleBoth(t, p, b, c, fixture.Select("SELECT id,r FROM "+table+" ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGROLE"}, want))
		}
		regroleBoth(t, p, b, c, fixture.Select("SELECT r FROM rr_null_view", []string{"r"}, []string{"REGROLE"}, [][]any{{nil}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if dump.Len() == 0 || strings.Contains(dump.String(), "CREATE ROLE") {
				t.Fatal("invalid role dump", dump.String())
			}
			nativeDump = bytes.Clone(dump.Bytes())
			held, err := c.QueryContext(p.Ctx, "SELECT id,r FROM rr_data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			metadata, err := held.ColumnTypes()
			if err != nil || len(metadata) != 2 || metadata[1].DatabaseTypeName() != "REGROLE" {
				t.Fatal("held metadata", metadata, err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE rr_replacement(v text)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_replacement VALUES('sentinel')", 1))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			fixture.ArrayReferenceConsumeHeld(t, held, want, []string{"INT4", "REGROLE"})
			check := func() {
				regroleControlChecks(t, p.Ctx, c, b.Recorder, want)
			}
			check()
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(nativeDump, redump.Bytes()) {
				t.Fatalf("unchanged redump differs\n--- source dump ---\n%s\n--- restored dump ---\n%s", nativeDump, redump.Bytes())
			}
			for _, script := range []string{
				"CREATE TABLE rr_forbidden(r regrole DEFAULT 'rr_role'::regrole);",
				"CREATE VIEW rr_forbidden AS SELECT 'rr_role'::regrole AS r;",
				regroleUnknownRestoreSQL,
			} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				if err == nil {
					t.Fatal("invalid Restore succeeded", script)
				}
				if strings.Contains(script, "rr_absent") {
					want := regroleUnknownRestoreDiagnostic()
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					want := fixture.ControlsReferenceDiagnostic("0A000", "constant of the type regrole cannot be used here")
					fixture.AssertDiagnostic(t, err, &want)
				}
				check()
			}
			ioFailure := errors.New("regrole late reader failure")
			input := io.MultiReader(strings.NewReader("CREATE TABLE rr_staged(v INT); INSERT INTO rr_staged VALUES(1);"), fixture.CatalogControlFailingReader{Err: ioFailure})
			if err := stuntgres.Restore(p.Ctx, p.Native, input); !errors.Is(err, ioFailure) {
				t.Fatal("reader error", err)
			}
			check()
			canceled, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(nativeDump), Cancel: cancel}
			if err := stuntgres.Restore(canceled, p.Native, reader); !errors.Is(err, context.Canceled) {
				t.Fatal("staged cancellation", err)
			}
			check()
			regroleCrossDriverRestore(t, p, c, nativeDump, want)
		} else {
			if len(nativeDump) == 0 {
				t.Fatal("missing actual native dump for PostgreSQL replay")
			}
			for _, q := range []string{"DROP VIEW rr_null_view,rr_data_view,rr_opaque_view", "DROP TABLE rr_data,rr_recursive,rr_numeric", "DROP DOMAIN rr_control_array"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			// The oracle exercises the same replay SQL. Native Restore retains
			// its full-source character positions and absent optional fields.
			result, err := c.ExecContext(p.Ctx, regroleUnknownRestoreSQL)
			if result != nil {
				t.Fatal("failed replay SQL published result", result)
			}
			simpleDiagnostic := regroleUnknownRestoreDiagnostic()
			fixture.AssertDiagnostic(t, err, &simpleDiagnostic)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP ROLE rr_role", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			result, err = c.ExecContext(p.Ctx, string(nativeDump))
			if _, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK"); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if result != nil {
				t.Fatal("failed actual dump published result", result)
			}
			missingDiagnostic := regroleMissingDumpDiagnostic(t, nativeDump)
			fixture.AssertDiagnostic(t, err, &missingDiagnostic)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE rr_role", 0))
			if _, err := c.ExecContext(p.Ctx, string(nativeDump)); err != nil {
				t.Fatal("PostgreSQL rejected actual native dump", err)
			}
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			regroleControlChecks(t, p.Ctx, c, b.Recorder, want)
		}
	})
}

func regroleControlChecks(t *testing.T, ctx context.Context, c fixture.Executor, recorder *fixture.Recorder, want [][]any) {
	t.Helper()
	for _, table := range []string{"rr_data", "rr_data_view"} {
		fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT id,r FROM "+table+" ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGROLE"}, want))
	}
	fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT r FROM rr_null_view", []string{"r"}, []string{"REGROLE"}, [][]any{{nil}}))
	fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT a,d::text AS d FROM rr_recursive", []string{"a", "d"}, []string{"_REGROLE", "TEXT"}, [][]any{{"{rr_role}", "{rr_role}"}}))
	fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT a FROM rr_opaque_view", []string{"a"}, []string{"_REGROLE"}, [][]any{{"{rr_role}"}}))
	fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT r FROM rr_numeric", []string{"r"}, []string{"REGROLE"}, [][]any{{"pg_monitor"}}))
	fixture.RunStep(t, ctx, c, recorder, fixture.Exec("INSERT INTO rr_data(id) VALUES(7)", 1))
	fixture.RunStep(t, ctx, c, recorder, fixture.Select("SELECT r FROM rr_data WHERE id=7", []string{"r"}, []string{"REGROLE"}, [][]any{{"rr_role"}}))
	fixture.RunStep(t, ctx, c, recorder, fixture.Exec("DELETE FROM rr_data WHERE id=7", 1))
}

func regroleCrossDriverRestore(t *testing.T, source *fixture.Pair, c *sql.Conn, dump []byte, want [][]any) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	for _, q := range []string{`CREATE ROLE rr_perturb_a`, `CREATE ROLE rr_perturb_b`, `CREATE ROLE "Mixed.Role"`, `CREATE ROLE rr_role`} {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
	}
	var sourceID, targetID int64
	if err := c.QueryRowContext(source.Ctx, "SELECT 'rr_role'::regrole::oid::bigint").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT 'rr_role'::regrole::oid::bigint").Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	if sourceID == targetID {
		t.Fatal("role allocator perturbation did not change identity")
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	regroleControlChecks(t, target.Ctx, target.Memory, target.MemoryRecord, want)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT r::oid::bigint AS id FROM rr_data WHERE id=1", []string{"id"}, []string{"INT8"}, [][]any{{targetID}}))
	missing := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, missing.Ctx, missing.Memory, missing.MemoryRecord, fixture.Exec("CREATE TABLE rr_sentinel(v text)", 0))
	fixture.RunStep(t, missing.Ctx, missing.Memory, missing.MemoryRecord, fixture.Exec("INSERT INTO rr_sentinel VALUES('preserved')", 1))
	err := stuntgres.Restore(missing.Ctx, missing.Native, bytes.NewReader(dump))
	missingDiagnostic := regroleMissingDumpDiagnostic(t, dump)
	fixture.AssertDiagnostic(t, err, &missingDiagnostic)
	fixture.RunStep(t, missing.Ctx, missing.Memory, missing.MemoryRecord, fixture.Select("SELECT v FROM rr_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"preserved"}}))
}
