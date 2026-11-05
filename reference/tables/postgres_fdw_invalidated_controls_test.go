package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These are genuine native host controls, not a fictional PostgreSQL Reset or
// Restore counterpart. Each case has its own serial scenario and real sessions.
func TestReferencePostgresFDWInvalidatedControls(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("native replacement ownership requires REFERENCE=1 and genuine remote backends")
	}
	for _, control := range []string{"reset", "restore"} {
		for _, invalidation := range []string{"private_server", "private_mapping", "peer_server", "peer_mapping"} {
			t.Run(control+"/"+invalidation, func(t *testing.T) {
				postgresFDWInvalidatedControl(t, control, invalidation)
			})
		}
	}
}

func postgresFDWInvalidatedControl(t *testing.T, control, invalidation string) {
	t.Helper()
	names := []string{"pf_replace_target", "pf_replace_peer"}
	fixture.DatabaseReferenceCleanup(t, &names)
	p, provider := postgresFDWPhysicalScenario(t)
	b := externalSQLNativeBackend(p)
	for _, name := range names {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE "+name, 0))
	}
	targetDB, targetRaw, targetRecord := fixture.DatabaseScenarioPool(t, p, false, names[0], "")
	otherDB, otherRaw, otherRecord := fixture.DatabaseScenarioPool(t, p, false, names[1], "")
	var c, other, publisher *sql.Conn
	// Even the expected red assertion must settle local transactions and close
	// physical pools before removing database metadata or entering the next case.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, conn := range []*sql.Conn{c, other, publisher} {
			if conn == nil {
				continue
			}
			if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Log("retired transaction cleanup rollback:", err)
			}
			if err := conn.Close(); err != nil {
				t.Error("physical connection cleanup:", err)
			}
		}
		for _, db := range []*sql.DB{targetDB, targetRaw, otherDB, otherRaw} {
			if err := db.Close(); err != nil {
				t.Error("physical pool cleanup:", err)
			}
		}
		for _, wire := range provider.Snapshot() {
			if wire.Activity().Closes == 0 {
				if err := wire.Close(ctx); err != nil {
					t.Error("remaining genuine wire cleanup:", err)
				}
			}
		}
		for _, name := range names {
			if _, err := b.DB.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
				t.Error("database cleanup:", err)
			}
		}
	}()
	var err error
	c, err = targetDB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	other, err = otherDB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, targetRecord, step) }
	runOther := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, other, otherRecord, step) }
	postgresFDWPhysicalInstall(t, b, run)
	postgresFDWPhysicalInstall(t, b, runOther)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, targetRaw, &dump); err != nil {
		t.Fatal(err)
	}
	if provider.OpenCalls() != 0 {
		t.Fatal("metadata setup/Dump opened transport")
	}
	runOther(fixture.Exec("BEGIN", 0))
	otherPID := postgresFDWPID(t, p, other)
	run(fixture.Exec("BEGIN", 0))
	pid := postgresFDWPID(t, p, c)
	if pid == otherPID {
		t.Fatal("independent databases shared a physical remote backend")
	}
	var owned *fixture.ExternalSQLWire
	for _, wire := range provider.Snapshot() {
		if wire.Info().BackendPID == pid {
			owned = wire
		}
	}
	if owned == nil {
		t.Fatal("actual active remote backend has no transport witness")
	}
	active := func(label string, expectedPID int32) {
		t.Helper()
		var n int64
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1 AND xact_start IS NOT NULL", expectedPID).Scan(&n); err != nil || n != 1 {
			t.Fatal(label, "lost actual remote transaction", expectedPID, n, err)
		}
	}
	preserved := func(label string) {
		t.Helper()
		active(label, pid)
		if owned.Activity().Closes != 0 {
			t.Fatal(label, "closed the incumbent physical resource", owned.Activity())
		}
	}
	active("before invalidation", pid)
	query := "ALTER SERVER pf_server OPTIONS(SET application_name 'pf_next_incarnation')"
	if strings.HasSuffix(invalidation, "mapping") {
		query = "ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password 'stuntgres')"
	}
	if strings.HasPrefix(invalidation, "peer_") {
		publisher, err = targetDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, publisher, targetRecord, fixture.Exec(query, 0))
	} else {
		run(fixture.Exec(query, 0))
	}
	preserved("ordinary in-use invalidation")
	before := provider.OpenCalls()
	if err := stuntgres.Restore(p.Ctx, targetRaw, strings.NewReader(dump.String()+"\nSELECT missing_invalidated_restore_name;")); err == nil {
		t.Fatal("failed Restore accepted")
	}
	preserved("failed Restore")
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Reset(canceled, targetRaw); err == nil {
		t.Fatal("canceled Reset accepted")
	}
	preserved("canceled Reset")
	if err := stuntgres.Restore(canceled, targetRaw, strings.NewReader(dump.String())); err == nil {
		t.Fatal("canceled Restore accepted")
	}
	preserved("canceled Restore")
	if provider.OpenCalls() != before {
		t.Fatal("failed/canceled controls opened transport")
	}
	if control == "reset" {
		err = stuntgres.Reset(p.Ctx, targetRaw)
	} else {
		err = stuntgres.Restore(p.Ctx, targetRaw, strings.NewReader(dump.String()))
	}
	if err != nil {
		t.Fatal(control, err)
	}
	// Observe independently immediately after return. A missing Close fails
	// immediately, rather than consuming the entire scenario deadline. A genuine
	// Close can precede PostgreSQL processing its socket exit, so only that case
	// receives the short bounded backend-disappearance observer.
	var remaining int64
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if owned.Activity().Closes != 1 {
		t.Fatalf("%s returned before invalidated owned connection Close: pid=%d backends=%d activity=%+v", control, pid, remaining, owned.Activity())
	}
	if remaining != 0 {
		ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
		defer cancel()
		externalSQLBackendGone(t, ctx, pid)
	}
	if got := postgresFDWPID(t, p, other); got != otherPID {
		t.Fatal("selected-database replacement retired another database's backend", otherPID, got)
	}
	active("independent database after replacement", otherPID)
	if provider.OpenCalls() != before {
		t.Fatal("replacement opened transport or replaced the independent database's connection")
	}
	if _, err := owned.Execute(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("retired old-incarnation transport still executed commands")
	}
	// Later settlement must remain idempotent. A stale old transaction can
	// report its ordinary retirement error; it cannot reopen or double-close
	// the old handle, and the same physical SQL session must recover.
	if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
		t.Log("retired transaction rollback:", err)
	}
	if owned.Activity().Closes != 1 {
		t.Fatal("old transaction settlement closed transport twice", owned.Activity())
	}
	if control == "reset" {
		if err := stuntgres.Restore(p.Ctx, targetRaw, strings.NewReader(dump.String())); err != nil {
			t.Fatal("reinstall after Reset:", err)
		}
	}
	if fresh := postgresFDWPID(t, p, c); fresh == pid {
		t.Fatal("same physical SQL owner resurrected the retired backend", pid)
	}
	if got := postgresFDWPID(t, p, other); got != otherPID {
		t.Fatal("fresh target scan disturbed the independent database", otherPID, got)
	}
	if provider.OpenCalls() != before+1 {
		t.Fatal("recovered owner did not establish exactly one genuine fresh connection")
	}
	if owned.Activity().Closes != 1 {
		t.Fatal("reanalysis double-closed old transport", owned.Activity())
	}
}
