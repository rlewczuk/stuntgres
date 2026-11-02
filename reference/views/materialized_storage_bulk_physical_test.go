package reference_test

import (
	"context"
	"database/sql"
	"io"
	"testing"

	"stuntgres/reference/fixture"
)

// Physical relocation exists only on the real server. The shared native phase
// asserts its real builtin boundary, never fake custom tablespaces or movement.
func materializedStoragePhysical(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) bool {
	t.Helper()
	if !b.PG {
		run(fixture.Select("SELECT spcname FROM pg_tablespace ORDER BY oid", []string{"spcname"}, []string{"NAME"}, [][]any{{"pg_default"}, {"pg_global"}}))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src SET TABLESPACE ms_dst NOWAIT", fixture.ControlsReferenceDiagnostic("42704", `tablespace "ms_src" does not exist`)))
		return false
	}
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", "mkdir /tmp/ms_src /tmp/ms_dst && chown postgres:postgres /tmp/ms_src /tmp/ms_dst"})
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(out)
	if err != nil || code != 0 {
		t.Fatal(code, string(text), err)
	}
	t.Cleanup(func() {
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP MATERIALIZED VIEW IF EXISTS information_schema.ms_p_info CASCADE", "DROP MATERIALIZED VIEW IF EXISTS ms_p_a,ms_p_b,ms_p_other,ms_p_admin CASCADE", "DROP VIEW IF EXISTS ms_p_view", "DROP TABLE IF EXISTS ms_p_table CASCADE", "DROP TABLESPACE IF EXISTS ms_src", "DROP TABLESPACE IF EXISTS ms_dst"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error(q, err)
			}
		}
		code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", "rmdir /tmp/ms_src /tmp/ms_dst"})
		if err != nil {
			t.Error(err)
			return
		}
		text, err := io.ReadAll(out)
		if err != nil || code != 0 {
			t.Error(code, string(text), err)
		}
	})
	run(fixture.Exec("CREATE TABLESPACE ms_src LOCATION '/tmp/ms_src'", 0))
	run(fixture.Exec("CREATE TABLESPACE ms_dst LOCATION '/tmp/ms_dst'", 0))
	materializedStorageRoles(run)
	run(fixture.Exec("GRANT CREATE ON TABLESPACE ms_src,ms_dst TO ms_owner,ms_other,ms_member", 0))
	return true
}

func materializedStoragePhysicalSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE ms_p_table(id int) TABLESPACE ms_src", 0))
	run(fixture.Exec("INSERT INTO ms_p_table VALUES(1),(2)", 2))
	run(fixture.Exec("CREATE INDEX ms_p_table_idx ON ms_p_table(id) TABLESPACE ms_src", 0))
	run(fixture.Exec("CREATE VIEW ms_p_view AS SELECT id FROM ms_p_table", 0))
	run(fixture.Exec("GRANT SELECT ON ms_p_table TO ms_owner,ms_other", 0))
	run(fixture.Exec("SET ROLE ms_owner", 0))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_p_a TABLESPACE ms_src AS SELECT id FROM ms_p_table", 2))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_p_b TABLESPACE ms_src AS SELECT id FROM ms_p_table WITH NO DATA", 0))
	run(fixture.Exec("CREATE INDEX ms_p_a_idx ON ms_p_a(id) TABLESPACE ms_src", 0))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("SET ROLE ms_other", 0))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_p_other TABLESPACE ms_src AS SELECT id FROM ms_p_table", 2))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_p_admin TABLESPACE ms_src AS SELECT id FROM ms_p_table", 2))
	run(fixture.Exec("CREATE MATERIALIZED VIEW information_schema.ms_p_info TABLESPACE ms_src AS SELECT id FROM ms_p_table", 2))
}

func materializedStoragePlacement(name, space string) fixture.Step {
	return fixture.Select("SELECT s.spcname FROM pg_class c JOIN pg_tablespace s ON s.oid=c.reltablespace WHERE c.oid='"+name+"'::regclass", []string{"spcname"}, []string{"NAME"}, [][]any{{space}})
}

func materializedStoragePhysicalRows(run func(fixture.Step)) {
	for _, name := range []string{"ms_p_a", "ms_p_other", "ms_p_admin", "ms_p_table", "ms_p_view"} {
		run(fixture.Select("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	}
	run(fixture.MaterializedViewUnpopulated("ms_p_b"))
	fixture.MaterializedViewPopulation(run, "ms_p_b", false)
}

func TestReferenceMaterializedStorageBulkPhysicalSelection(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		if !materializedStoragePhysical(t, p, b, c, run) {
			return
		}
		materializedStoragePhysicalSeed(run)
		run(fixture.Exec("SET ROLE ms_member", 0))
		// Membership grants ownership authority, never expands OWNED BY's OIDs.
		run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY ms_member SET TABLESPACE ms_dst", 0))
		run(materializedStoragePlacement("ms_p_a", "ms_src"))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY ms_owner,ms_owner SET TABLESPACE ms_dst", 0))
		run(fixture.Exec("RESET ROLE", 0))
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			run(materializedStoragePlacement(name, "ms_dst"))
		}
		for _, name := range []string{"ms_p_other", "ms_p_admin", "ms_p_a_idx", "ms_p_table", "ms_p_table_idx", "information_schema.ms_p_info"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
		materializedStoragePhysicalRows(run)
		run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_dst SET TABLESPACE ms_src", 0))
		// These actual held utilities resolve each role placeholder at Execute,
		// not under the owner present during Prepare.
		for _, identity := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			run(fixture.Exec("SET ROLE ms_owner", 0))
			q := "ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY " + identity + " SET TABLESPACE ms_dst"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			executor := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE ms_other", 0))
			if identity == "SESSION_USER" {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view ms_p_admin")))
			} else {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(materializedStoragePlacement("ms_p_a", "ms_src"))
			if identity == "SESSION_USER" {
				run(materializedStoragePlacement("ms_p_other", "ms_src"))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
				run(materializedStoragePlacement("ms_p_admin", "ms_dst"))
				run(materializedStoragePlacement("information_schema.ms_p_info", "ms_dst"))
			} else {
				run(materializedStoragePlacement("ms_p_other", "ms_dst"))
				run(materializedStoragePlacement("ms_p_admin", "ms_src"))
			}
			s.Close()
			run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_dst SET TABLESPACE ms_src", 0))
		}
		run(fixture.Exec("SET ROLE ms_reader", 0))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY ms_reader SET TABLESPACE ms_dst", fixture.ControlsReferenceDiagnostic("42501", "permission denied for tablespace ms_dst")))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src SET TABLESPACE ms_dst", 0))
		for _, name := range []string{"ms_p_a", "ms_p_b", "ms_p_other", "ms_p_admin", "information_schema.ms_p_info"} {
			run(materializedStoragePlacement(name, "ms_dst"))
		}
		for _, name := range []string{"ms_p_a_idx", "ms_p_table", "ms_p_table_idx"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
		materializedStoragePhysicalRows(run)
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, source := range []string{"ms_src", "pg_default"} {
			notices = nil
			fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE "+source+" OWNED BY ms_reader SET TABLESPACE ms_dst", 0))
			label := source
			if source == "pg_default" {
				label = "(database default)"
			}
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "P0002", Message: `no matching relations in tablespace "` + label + `" found`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("empty distinct notices %+v want %+v", notices, want)
			}
		}
	})
}

func TestReferenceMaterializedStorageBulkPhysicalAtomicity(t *testing.T) {
	materializedStorageRoutesMode(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		if !materializedStoragePhysical(t, p, b, c, run) {
			return
		}
		materializedStoragePhysicalSeed(run)
		allSource := func() {
			for _, name := range []string{"ms_p_a", "ms_p_b", "ms_p_other", "ms_p_admin", "information_schema.ms_p_info"} {
				run(materializedStoragePlacement(name, "ms_src"))
			}
			materializedStoragePhysicalRows(run)
		}
		// Valid selected objects preceding an unauthorized owner cannot move.
		run(fixture.Exec("SET ROLE ms_member", 0))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY ms_owner,ms_other SET TABLESPACE ms_dst", fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view ms_p_other")))
		run(fixture.Exec("RESET ROLE", 0))
		allSource()
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		workerPID, holderPID := fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
		oid := fixture.LockOID(t, p.Ctx, "ms_p_b")
		// Populate the second selected relation before retaining an actual
		// streaming reader and its clean transaction's AccessShare grant.
		run(fixture.Exec("REFRESH MATERIALIZED VIEW ms_p_b", 0))
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		retained, err := tx.QueryContext(p.Ctx, "SELECT id FROM ms_p_b ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.TableDDLColumnTypes(t, true, columns, []string{"id"}, []string{"INT4"}, []bool{true})
		if !retained.Next() {
			t.Fatal("retained first row absent", retained.Err())
		}
		var first int64
		if err := retained.Scan(&first); err != nil || first != 1 {
			t.Fatal(first, err)
		}
		q := "ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src OWNED BY ms_owner SET TABLESPACE ms_dst"
		// Exactly one blocked selected relation makes the NOWAIT name stable;
		// the earlier selected MV remains source even if its lock was acquired.
		run(fixture.ErrorDiagnostic(q+" NOWAIT", fixture.ControlsReferenceDiagnostic("55006", `aborting because lock on relation "public.ms_p_b" is not available`)))
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
		var held *sql.Stmt
		if prepared {
			held, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			var result sql.Result
			var err error
			if prepared {
				result, err = held.ExecContext(ctx)
			} else {
				result, err = c.ExecContext(ctx, q)
			}
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "AccessExclusiveLock", finished)
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, materializedStoragePlacement(name, "ms_src"))
		}
		var ok bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
			t.Fatal(ok, err)
		}
		out := join()
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, out.Err, &want)
		if out.Result != nil || out.Rows != nil {
			t.Fatal("canceled bulk exposed output")
		}
		if !retained.Next() {
			t.Fatal("retained second row absent", retained.Err())
		}
		var second int64
		if err := retained.Scan(&second); err != nil || second != 2 {
			t.Fatal(second, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained suffix changed", retained.Err())
		}
		retained.Close()
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ms_p_a", "ms_p_b", "ms_p_other", "ms_p_admin", "information_schema.ms_p_info"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
		run(fixture.Select("SELECT id FROM ms_p_a ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT count(*) FROM pg_locks WHERE NOT granted", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		var executor fixture.Executor = c
		if prepared {
			executor = fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		}
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			run(materializedStoragePlacement(name, "ms_dst"))
		}
		run(fixture.Exec("ROLLBACK", 0))
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
		for _, name := range []string{"ms_p_a", "ms_p_b"} {
			run(materializedStoragePlacement(name, "ms_dst"))
		}
		for _, name := range []string{"ms_p_a_idx", "ms_p_other", "ms_p_table"} {
			run(materializedStoragePlacement(name, "ms_src"))
		}
	})
}
