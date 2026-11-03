package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ksLifeUnsafeSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) freezeHeader {
	t.Helper()
	ksAuditSetup(t, p, b, c)
	for _, q := range []string{
		"ALTER TABLE ks_part.heap ADD PRIMARY KEY(k)",
		"ALTER TABLE ks_part.heap ADD CONSTRAINT positive CHECK(k>0)",
		"ALTER TABLE ks_part.heap ALTER COLUMN v SET NOT NULL",
		"CREATE TABLE ks_part.child(k integer CONSTRAINT ks_fk REFERENCES ks_part.heap(k) ON DELETE CASCADE)",
		"CREATE INDEX kill_idx ON ks_part.heap(v)",
	} {
		freezeRun(t, p, b, c, fixture.Exec(q, 0))
	}
	freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(1,'killed'),(2,'survivor'),(3,'delete_control')", 3))
	freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.child VALUES(1),(3)", 2))
	// A real ordinary DELETE proves both the audit trigger and CASCADE work.
	freezeRun(t, p, b, c, fixture.Exec("DELETE FROM ks_part.heap WHERE k=3", 1))
	freezeRun(t, p, b, c, fixture.Select("SELECT k FROM ks_part.audit", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
	freezeRun(t, p, b, c, fixture.Select("SELECT k FROM ks_part.child", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	freezeRun(t, p, b, c, fixture.Exec("DELETE FROM ks_part.audit", 1))
	return freezeOne(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM ks_part.heap WHERE k=1")
}

func ksLifeUnsafeState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ksLifeRows(t, p, b, c, []any{int64(2), "survivor"})
	freezeBoth(t, p, b, c, fixture.Select("SELECT k FROM ks_part.child ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	freezeBoth(t, p, b, c, fixture.Select("SELECT k FROM ks_part.audit", []string{"k"}, []string{"INT4"}, nil))
	freezeRun(t, p, b, c, fixture.Select("SELECT convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.child'::regclass AND conname='ks_fk'", []string{"convalidated", "conenforced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conrelid='ks_part.child'::regclass AND conname='ks_fk') AND tgenabled='O'", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
	freezeRun(t, p, b, c, fixture.Select("SELECT convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass AND conname='positive'", []string{"convalidated", "conenforced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
}

func ksLifeFKError(key string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("23503", `insert or update on table "child" violates foreign key constraint "ks_fk"`)
	want.Detail = `Key (k)=(` + key + `) is not present in table "heap".`
	want.Schema, want.Table, want.Constraint = "ks_part", "child", "ks_fk"
	return want
}

func TestReferencePgsurgeryKillControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ks_control_owner", "ks_control_denied"})
	ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := ksLifeUnsafeSetup(t, p, b, c)
		ksLifeApply(t, p, b, c, a.tid)
		ksLifeUnsafeState(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE ks_control_owner", "CREATE ROLE ks_control_denied", "CREATE SCHEMA ks_moved",
			"CREATE VIEW ks_part.kill_null AS SELECT ks_part.heap_force_kill(NULL::regclass,NULL::tid[]) IS NULL AS strict_null",
			"ALTER TABLE ks_part.heap OWNER TO ks_control_owner",
			"GRANT USAGE ON SCHEMA ks_part,ks_moved TO ks_control_owner,ks_control_denied",
			"GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_control_owner",
			"ALTER FUNCTION ks_part.heap_force_kill(regclass,tid[]) RENAME TO remove_version",
			"ALTER FUNCTION ks_part.remove_version(regclass,tid[]) SET SCHEMA ks_moved",
		} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			bb := b
			bb.Recorder = rec
			ksLifeUnsafeState(t, p, bb, target)
			freezeBoth(t, p, bb, target, fixture.Select("SELECT strict_null FROM ks_part.kill_null", []string{"strict_null"}, []string{"BOOL"}, [][]any{{true}}))
			freezeRun(t, p, bb, target, fixture.Select("SELECT r.rolname::text AS owner FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='ks_part.heap'::regclass", []string{"owner"}, []string{"TEXT"}, [][]any{{"ks_control_owner"}}))
			freezeRun(t, p, bb, target, fixture.Select("SELECT n.nspname::text AS schema,p.proname::text AS name,p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_surgery' ORDER BY p.prosrc", []string{"schema", "name", "prosrc", "probin"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ks_part", "heap_force_freeze", "heap_force_freeze", "$libdir/pg_surgery"}, {"ks_moved", "remove_version", "heap_force_kill", "$libdir/pg_surgery"}}))
			freezeRun(t, p, bb, target, fixture.Select("SELECT has_function_privilege('ks_control_owner','ks_moved.remove_version(regclass,tid[])','EXECUTE') AS allowed,has_function_privilege('ks_control_denied','ks_moved.remove_version(regclass,tid[])','EXECUTE') AS denied,has_function_privilege('ks_control_owner','ks_part.heap_force_freeze(regclass,tid[])','EXECUTE') AS freeze_grant", []string{"allowed", "denied", "freeze_grant"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}}))
			q := "SELECT ks_moved.remove_version($1::regclass,$2::tid[]) AS killed"
			freezeRun(t, p, bb, target, fixture.Exec("SET SESSION AUTHORIZATION ks_control_owner", 0))
			defer target.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			freezeBoth(t, p, bb, target, fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", "{}"))
			freezeBoth(t, p, bb, target, fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, nil, nil))
			freezeRun(t, p, bb, target, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			freezeBoth(t, p, bb, target, fixture.ImplicitError("INSERT INTO ks_part.child VALUES(99)", ksLifeFKError("99")))
		}
		post(c, b.Recorder)
		if b.PG {
			return
		} // Native control replacement has no server analogue.
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		old, err := c.QueryContext(p.Ctx, "SELECT k,v FROM ks_part.heap ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		ksLifeDetachedRows(t, old, [][]any{{int64(2), "survivor"}})
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal("sealed orphan snapshot", err)
		}
		post(c, b.Recorder)
		// Rebuilt logical indexes must not serialize physical killed members.
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION amcheck SCHEMA ks_part", 0))
		ksLifeCheck(t, p, b, c)
		freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION amcheck", 0))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore changed provider/catalog/data")
		}
		ioErr := errors.New("kill restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, ksLifeBrokenReader{ioErr}); !errors.Is(err, ioErr) {
			t.Fatal("restore reader error", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed I/O restore changed provider/catalog/data")
		}
		other, dest, rec := pgcryptoControlDestination(t, p, "ks_kill_destination")
		for _, role := range []string{"ks_control_owner", "ks_control_denied"} {
			fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal("independent driver sealed snapshot", err)
		}
		post(dest, rec)
		fresh := freezeOne(t, p, dest, "SELECT k,v,ctid,xmin,xmax FROM ks_part.heap WHERE k=2")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT ks_moved.remove_version($1::regclass,$2::tid[]) AS killed", []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", freezeTIDs(fresh.tid)))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM ks_part.heap", []string{"k"}, []string{"INT4"}, nil))
		post(c, b.Recorder) // Independent driver mutation must not leak back.
		// Alter real emitted SQL, leaving orphan intact but invalidating its
		// authenticated snapshot seal. Ordinary historical FK validation applies.
		edited := strings.Replace(dump, "'survivor'", "'edited'", 1)
		if edited == dump {
			t.Fatal("dump omitted independently expected survivor literal")
		}
		sentinelDB, sentinel, sentinelRec := pgcryptoControlDestination(t, p, "ks_kill_rejected_destination")
		for _, q := range []string{"CREATE ROLE ks_control_owner", "CREATE ROLE ks_control_denied", "CREATE EXTENSION pg_surgery", "CREATE TABLE sentinel(k integer)", "INSERT INTO sentinel VALUES(77)"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 1
			}
			fixture.RunStep(t, p.Ctx, sentinel, sentinelRec, fixture.Exec(q, count))
		}
		sentinelBefore := fixture.ExtensionDump(t, p.Ctx, sentinelDB)
		err = stuntgres.Restore(p.Ctx, sentinelDB, strings.NewReader(edited))
		want := ksLifeFKError("1")
		fixture.AssertDiagnostic(t, err, &want)
		if fixture.ExtensionDump(t, p.Ctx, sentinelDB) != sentinelBefore {
			t.Fatal("edited snapshot failed to preserve destination provider/catalog/data atomically")
		}
		fixture.RunStep(t, p.Ctx, sentinel, sentinelRec, fixture.Select("SELECT k FROM sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(77)}}))
		fixture.RunStep(t, p.Ctx, sentinel, sentinelRec, fixture.Select("SELECT public.heap_force_kill(NULL::regclass,NULL::tid[]) AS killed", []string{"killed"}, []string{"VOID"}, [][]any{{nil}}))
	})
	t.Run("prepared_control_gates_and_snapshot_omissions", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'killed') RETURNING k,v,ctid,xmin,xmax")
			freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
			ksLifeApply(t, p, b, c, a.tid)
			if b.PG {
				return
			}
			baseline := fixture.ExtensionDump(t, p.Ctx, p.Native)
			writer := freezeConn(t, p, b)
			defer writer.Close()
			defer writer.ExecContext(p.Ctx, "ROLLBACK")
			defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED 'ks_control_prepared'")
			freezeRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, writer, fixture.Exec("INSERT INTO ks_part.heap VALUES(3,'private')", 1))
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != baseline {
				t.Fatal("private writes leaked into kill snapshot")
			}
			freezeRun(t, p, b, writer, fixture.Exec("PREPARE TRANSACTION 'ks_control_prepared'", 0))
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != baseline {
				t.Fatal("prepared writes/GID/locks leaked into kill snapshot")
			}
			for _, replace := range []func() error{
				func() error { return stuntgres.Reset(p.Ctx, p.Native) },
				func() error { return stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline)) },
			} {
				fixture.TransactionOrdinary(t, replace(), "stuntgres: cannot replace catalog while prepared transactions exist")
				ksLifeRows(t, p, b, c, []any{int64(2), "survivor"})
				freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_prepared_xacts WHERE gid='ks_control_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
			freezeRun(t, p, b, c, fixture.Exec("ROLLBACK PREPARED 'ks_control_prepared'", 0))
		})
	})
}

type ksLifeBrokenReader struct{ err error }

func (r ksLifeBrokenReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferencePgsurgeryKillFeatureSmoke(t *testing.T) {
	ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		a := ksLifeUnsafeSetup(t, p, b, c)
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION amcheck SCHEMA ks_part", 0))
		defer c.ExecContext(p.Ctx, "DROP EXTENSION amcheck CASCADE")
		ksLifeCheck(t, p, b, c)
		st, err := c.PrepareContext(p.Ctx, killSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: killSQL}
		fixture.RunStep(t, p.Ctx, held, rec, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", freezeTIDs(a.tid)))
		ksLifeUnsafeState(t, p, b, c)
		freezeBoth(t, p, b, c, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, nil, nil))
		notices = nil
		fixture.RunStep(t, p.Ctx, held, rec, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", freezeTIDs(a.tid)))
		ksLifeDeadNotice(t, notices, a.tid)
		freezeBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO ks_part.child VALUES(99)", ksLifeFKError("99")))
		if b.PG {
			return
		} // Real physical HOT is in the independent Amcheck family.
		step := fixture.ImplicitError(ksLifeCheckSQL, ksLifeLogicalCorruption("kill_idx"))
		step.SelectRows = true
		freezeBoth(t, p, b, c, step)
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		ksLifeUnsafeState(t, p, b, c)
		ksLifeCheck(t, p, b, c)
		freezeBoth(t, p, b, c, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", "{}"))
		freezeBoth(t, p, b, c, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, nil, nil))
	})
}
