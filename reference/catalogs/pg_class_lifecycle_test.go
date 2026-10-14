package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc18_life_owner", "pc18_life_reader"})
	fixture.SchemaReferenceCleanup(t, "pc18_life_moved", "pc18_life_shadow")
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE ROLE pc18_life_owner", "CREATE ROLE pc18_life_reader", "CREATE SCHEMA pc18_life_moved", "CREATE TABLE pc18_life_moved.pc18_life_anchor(id integer)", "CREATE TABLE pc18_life_heap(id integer)")
		q := "SELECT relname,relkind,relowner,relhasindex FROM pg_class WHERE oid=$1"
		var oid, owner int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pc18_life_heap'::regclass::oid,(SELECT oid FROM pg_roles WHERE rolname=$1)", pgTableViewsOwner(b)).Scan(&oid, &owner); err != nil {
			t.Fatal(err)
		}
		held := fixture.ViewHold(t, p, b, q)
		check := func(name string, own int64, index bool) {
			pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname", "relkind", "relowner", "relhasindex"}, []string{"NAME", "CHAR", "OID", "BOOL"}, [][]any{{name, "r", own, index}}, oid))
		}
		check("pc18_life_heap", owner, false)
		var old *sql.Rows
		var types []*sql.ColumnType
		if !b.PG {
			var err error
			old, err = b.DB.QueryContext(p.Ctx, q, oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err = old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		pgClassExec(t, p, b, b.DB, "CREATE INDEX pc18_life_idx ON pc18_life_heap(id)", "DROP INDEX pc18_life_idx", "ALTER TABLE pc18_life_heap RENAME TO pc18_life_renamed", "ALTER TABLE pc18_life_renamed SET SCHEMA pc18_life_moved", "ALTER TABLE pc18_life_moved.pc18_life_renamed OWNER TO pc18_life_owner")
		var newOwner int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pc18_life_owner'").Scan(&newOwner); err != nil {
			t.Fatal(err)
		}
		check("pc18_life_renamed", newOwner, true)
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT oid=$1 AS same_identity,relnamespace=(SELECT relnamespace FROM pg_class WHERE oid='pc18_life_moved.pc18_life_anchor'::regclass::oid) AS user_namespace,relowner=$2 AS owner FROM pg_class WHERE oid='pc18_life_moved.pc18_life_renamed'::regclass::oid", []string{"same_identity", "user_namespace", "owner"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, oid, newOwner))
		pgClassExec(t, p, b, b.DB, "DROP TABLE pc18_life_moved.pc18_life_renamed")
		pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname", "relkind", "relowner", "relhasindex"}, []string{"NAME", "CHAR", "OID", "BOOL"}, nil, oid))
		if old != nil {
			pgClassRetained(t, old, types, []string{"relname", "relkind", "relowner", "relhasindex"}, []string{"NAME", "CHAR", "OID", "BOOL"}, []any{"pc18_life_heap", "r", owner, false})
		}
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_life_moved.pc18_life_renamed(id integer)")
		pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname", "relkind", "relowner", "relhasindex"}, []string{"NAME", "CHAR", "OID", "BOOL"}, nil, oid))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT oid=$1 AS old_identity FROM pg_class WHERE oid='pc18_life_moved.pc18_life_renamed'::regclass::oid", []string{"old_identity"}, []string{"BOOL"}, [][]any{{false}}, oid))
		// Catalog privilege, not referenced table privilege, is checked on each
		// execution, including empty-result handles prepared before REVOKE.
		a, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, reader, "SET ROLE pc18_life_reader")
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		empty := "SELECT * FROM pg_catalog.pg_class WHERE FALSE"
		stmt, err := reader.PrepareContext(p.Ctx, empty)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		readerHeld := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: empty}
		pgClassCheck(t, p, b, readerHeld, fixture.Select(empty, pgClassColumns, pgClassTypes, nil))
		if b.PG {
			pgClassExec(t, p, b, a, "REVOKE SELECT ON pg_catalog.pg_class FROM PUBLIC")
			defer a.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_class TO PUBLIC")
			denied := fixture.ImplicitError(empty, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_class"})
			denied.SelectRows, denied.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, readerHeld, b.Recorder, denied)
			pgClassExec(t, p, b, a, "GRANT SELECT ON pg_catalog.pg_class TO PUBLIC")
		} else {
			// Catalog-source grant SQL remains outside native admission. The
			// actual SetObjectACL recheck is covered by TestPgClassCatalogACLRecheck.
			for _, query := range []string{"REVOKE SELECT ON pg_catalog.pg_class FROM PUBLIC", "GRANT SELECT ON pg_catalog.pg_class TO PUBLIC"} {
				unsupported := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "statement is not supported"})
				unsupported.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, a, b.Recorder, unsupported)
				pgClassCheck(t, p, b, readerHeld, fixture.Select(empty, pgClassColumns, pgClassTypes, nil))
			}
		}
		pgClassCheck(t, p, b, readerHeld, fixture.Select(empty, pgClassColumns, pgClassTypes, nil))
		// Unqualified source binding obeys existing explicit path semantics.
		pgClassExec(t, p, b, a, "CREATE SCHEMA pc18_life_shadow", "CREATE TABLE pc18_life_shadow.pg_class(marker integer)", "SET search_path=pc18_life_shadow,pg_catalog")
		pgClassCheck(t, p, b, a, fixture.Select("SELECT * FROM pg_class WHERE FALSE", []string{"marker"}, []string{"INT4"}, nil))
		pgClassCheck(t, p, b, a, fixture.Select(empty, pgClassColumns, pgClassTypes, nil))
		pgClassExec(t, p, b, a, "RESET search_path")
	})
}

func TestReferencePgClassTransactions(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, a, "CREATE SEQUENCE pc18_tx_seq", "CREATE VIEW pc18_tx_view AS SELECT last_value FROM pc18_tx_seq")
		q := "SELECT relname FROM pg_class WHERE relname='pc18_tx_seq' OR relname='pc18_tx_view' ORDER BY relname"
		check := func(db fixture.Executor, present bool) {
			var want [][]any
			if present {
				want = [][]any{{"pc18_tx_seq"}, {"pc18_tx_view"}}
			}
			pgClassCheck(t, p, b, db, fixture.Select(q, []string{"relname"}, []string{"NAME"}, want))
		}
		var retained *sql.Rows
		var retainedTypes []*sql.ColumnType
		if !b.PG {
			var err error
			retained, err = a.QueryContext(p.Ctx, "SELECT relname FROM pg_class WHERE relname='pc18_tx_seq'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			retainedTypes, err = retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		pgClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pc18_tx_save", "DROP SEQUENCE pc18_tx_seq CASCADE")
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		check(a, false)
		check(peer, true)
		pgClassExec(t, p, b, a, "ROLLBACK TO pc18_tx_save")
		check(a, true)
		pgClassExec(t, p, b, a, "ROLLBACK")
		check(peer, true)
		pgClassExec(t, p, b, a, "BEGIN", "DROP SEQUENCE pc18_tx_seq CASCADE", "COMMIT")
		check(peer, false)
		if retained != nil {
			pgClassRetained(t, retained, retainedTypes, []string{"relname"}, []string{"NAME"}, []any{"pc18_tx_seq"})
		}
		pgClassExec(t, p, b, a, "BEGIN", "CREATE TEMP TABLE pc18_tx_temp(id integer)")
		pgClassCheck(t, p, b, a, fixture.Select("SELECT relpersistence FROM pg_class WHERE relname='pc18_tx_temp'", []string{"relpersistence"}, []string{"CHAR"}, [][]any{{"t"}}))
		pgClassCheck(t, p, b, peer, fixture.Select("SELECT relpersistence FROM pg_class WHERE relname='pc18_tx_temp'", []string{"relpersistence"}, []string{"CHAR"}, nil))
		pgClassExec(t, p, b, a, "ROLLBACK")
		pgClassCheck(t, p, b, a, fixture.Select("SELECT relpersistence FROM pg_class WHERE relname='pc18_tx_temp'", []string{"relpersistence"}, []string{"CHAR"}, nil))
		pgClassExec(t, p, b, peer, "CREATE TABLE pc18_tx_old(id integer)")
		q = "SELECT relname FROM pg_class WHERE relname='pc18_tx_old' OR relname='pc18_tx_added' ORDER BY relname"
		stmt, err := a.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
		rr := func(db fixture.Executor, added bool) {
			want := [][]any{{"pc18_tx_old"}}
			if added {
				want = [][]any{{"pc18_tx_added"}, {"pc18_tx_old"}}
			}
			pgClassCheck(t, p, b, db, fixture.Select(q, []string{"relname"}, []string{"NAME"}, want))
		}
		pgClassExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		rr(a, false)
		rr(held, false)
		pgClassExec(t, p, b, peer, "CREATE TABLE pc18_tx_added(id integer)")
		rr(a, !b.PG)
		rr(held, !b.PG)
		rr(peer, true)
		pgClassExec(t, p, b, a, "COMMIT")
		rr(a, true)
		rr(held, true)
		pgClassExec(t, p, b, a, "BEGIN")
		missing := fixture.ImplicitError("SELECT missing FROM pg_class", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, a, b.Recorder, missing)
		aborted := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.SelectRows, aborted.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aborted)
		pgClassExec(t, p, b, a, "ROLLBACK")
		rr(held, true)
	})
}

func TestReferencePgClassTemporary(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Dedicated case pool ensures closing the owner closes its physical
		// session, rather than merely returning a borrower to the idle pool.
		b.DB.SetMaxIdleConns(0)
		owner, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		pgClassExec(t, p, b, owner, "CREATE TEMP TABLE pc18_temp_heap(id integer)", "CREATE INDEX pc18_temp_idx ON pc18_temp_heap(id)", "CREATE TEMP SEQUENCE pc18_temp_seq", "CREATE TEMP VIEW pc18_temp_view AS SELECT id FROM pc18_temp_heap")
		q := "SELECT relname,relpersistence,relnamespace=(SELECT relnamespace FROM pg_class WHERE relname='pc18_temp_heap') AS same_namespace FROM pg_class WHERE " + pgTableViewsNames("relname", []string{"pc18_temp_heap", "pc18_temp_idx", "pc18_temp_seq", "pc18_temp_view"}) + " ORDER BY relname"
		want := [][]any{{"pc18_temp_heap", "t", true}, {"pc18_temp_idx", "t", true}, {"pc18_temp_seq", "t", true}, {"pc18_temp_view", "t", true}}
		s := fixture.Select(q, []string{"relname", "relpersistence", "same_namespace"}, []string{"NAME", "CHAR", "BOOL"}, want)
		pgClassCheck(t, p, b, owner, s)
		pgClassCheck(t, p, b, peer, s)
		noAccess := fixture.ImplicitError("SELECT * FROM pc18_temp_heap", fixture.Diagnostic{Code: "42P01", Message: `relation "pc18_temp_heap" does not exist`, Position: "15"})
		noAccess.SelectRows, noAccess.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, noAccess)
		// Close initiates PostgreSQL teardown; wait for actual backend exit,
		// not for guessed timing or a weakened catalog bag. Retire preserves
		// native synchronous close and uses the shared bounded PID barrier.
		session := fixture.TempReferenceSession{T: t, P: p, DB: b.DB, C: owner, Peer: peer, Record: b.Recorder, Postgres: b.PG}
		session.Retire()
		s.Rows = nil
		pgClassCheck(t, p, b, peer, s)
	})
}
