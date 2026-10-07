//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceSequenceTempMove(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}} {
				t.Run(backend.name, func(t *testing.T) {
					diagnostic := func(err error, code, message, detail, position string) {
						want := controlsReferenceDiagnostic(code, message)
						want.Detail, want.Position = detail, position
						referenceAssertDiagnostic(t, err, &want)
					}
					sequenceTempMoveWitness(t, p.ctx, backend.db, prepared, backend.name == "native", diagnostic)
				})
			}
		})
	}
}

// Only PostgreSQL exposes pg_namespace here. Do not fabricate a native catalog
// query or equate the pg_temp alias with either backend's generated schema name.
func TestReferenceSequenceTempMoveNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			exec := func(q string) {
				sequenceProbeExec(t, p.ctx, c, q)
			}
			exec("CREATE TEMP SEQUENCE actual_temp START 31 CACHE 5")
			exec("CREATE SEQUENCE public.actual_persistent START 51 CACHE 5")
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_temp')", 31)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('public.actual_persistent')", 51)
			var namespace string
			var oid int64
			var pid int
			if err := c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			if namespace == "" || namespace == "pg_temp" {
				t.Fatal("expected actual generated namespace", namespace)
			}
			if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal(oid, err)
			}
			for _, q := range []string{
				"ALTER SEQUENCE public.actual_persistent SET SCHEMA " + namespace,
				"ALTER SEQUENCE " + namespace + ".actual_temp SET SCHEMA public",
			} {
				want := controlsReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas")
				referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, q, prepared), &want)
			}
			// A peer's actual namespace is independently temporary as well.
			sequenceProbeExec(t, p.ctx, peer, "CREATE TEMP SEQUENCE actual_peer START 81")
			var peerNamespace string
			if err := peer.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&peerNamespace); err != nil || peerNamespace == namespace {
				t.Fatal(namespace, peerNamespace, err)
			}
			want := controlsReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions")
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE public.actual_persistent SET SCHEMA "+peerNamespace, prepared), &want)
			// CREATE and SET SCHEMA consume the same release creation authority.
			// A foreign temporary namespace must never acquire the new object.
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "CREATE SEQUENCE "+peerNamespace+".forbidden_new", prepared), &want)
			var absent bool
			if err := peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", peerNamespace+".forbidden_new").Scan(&absent); err != nil || !absent {
				t.Fatal("foreign temporary CREATE published an object", absent, err)
			}
			var after int64
			if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&after); err != nil || after != oid {
				t.Fatal("identity changed", oid, after, err)
			}
			referenceGenericRenameState(t, p, c, namespace+".actual_temp", 35, true)
			referenceGenericRenameState(t, p, c, "public.actual_persistent", 55, true)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_temp')", 32)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('public.actual_persistent')", 52)
			// The release creation authority reports missing/denied destination
			// before considering a temporary source endpoint.
			exec("CREATE ROLE temp_move_oracle_role")
			defer func() {
				p.postgres.SetMaxIdleConns(0)
				_ = c.Close()
				if _, err := peer.ExecContext(p.ctx, "DROP ROLE temp_move_oracle_role"); err != nil {
					t.Error(err)
				}
			}()
			exec("SET ROLE temp_move_oracle_role")
			exec("CREATE TEMP SEQUENCE denied_temp START 91 CACHE 1")
			denied := controlsReferenceDiagnostic("42501", "permission denied for schema public")
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE denied_temp SET SCHEMA public", prepared), &denied)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('denied_temp')", 91)
			exec("RESET ROLE")
			exec("DROP SEQUENCE pg_temp.denied_temp")
			p.postgres.SetMaxIdleConns(0)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// Close releases the client before backend teardown necessarily finishes.
			// Observe the actual server exit, as in the existing TEMP lifecycle fixture.
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
			defer cancel()
			for {
				var live bool
				if err := peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
					t.Fatal("observing physical backend exit", err)
				}
				if !live {
					break
				}
			}
			// Exact owner identity has disappeared, while the peer's object and
			// persistent visibility survive. Namespace tuple retirement is not
			// asserted: PostgreSQL retains that tuple for backend-slot reuse.
			var removed bool
			if err := peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", namespace+".actual_temp").Scan(&removed); err != nil || !removed {
				t.Fatal("physical owner cleanup", removed, err)
			}
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('actual_peer')", 81)
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('public.actual_persistent')", 56)
		})
	}
}

// Rejected relocation must preserve both the temporary object's session state
// and unrelated retained source bindings; no sleep/deadline is used as proof.
func sequenceTempMoveWitness(t *testing.T, ctx context.Context, db *sql.DB, prepared, retainRows bool, diagnostic func(error, string, string, string, string)) {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	exec := func(conn *sql.Conn, q string) {
		r, err := conn.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(q, n, err)
		}
	}
	value := func(conn *sql.Conn, q string, want int64) {
		var got int64
		if err := conn.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatal(q, got, want, err)
		}
	}
	utility := func(q string) error {
		var result sql.Result
		var err error
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("utility Prepare", q, e)
			}
			defer s.Close()
			result, err = s.ExecContext(ctx)
		} else {
			result, err = c.ExecContext(ctx, q)
		}
		if err == nil {
			if n, e := result.RowsAffected(); e != nil || n != 0 {
				t.Fatal(q, n, e)
			}
		}
		return err
	}
	for _, q := range []string{
		"CREATE SCHEMA temp_move_a", "CREATE SCHEMA temp_move_b", "CREATE SCHEMA temp_move_dst",
		"CREATE SEQUENCE public.movable START 1000 CACHE 1",
		"CREATE TEMP SEQUENCE movable START 31 CACHE 5",
		"CREATE TEMP SEQUENCE owner_only START 71 CACHE 1",
		"CREATE SEQUENCE temp_move_b.s START 10 CACHE 1",
		"CREATE SEQUENCE public.persistent_move START 51 CACHE 5",
		"CREATE TABLE public.link_table(id INT)",
		"CREATE SEQUENCE public.linked OWNED BY public.link_table.id",
		"SET search_path TO temp_move_a,temp_move_b,public",
	} {
		exec(c, q)
	}
	defer func() {
		for _, schema := range []string{"temp_move_a", "temp_move_b", "temp_move_dst"} {
			if _, err := peer.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	// Peer owns a different temporary namespace, independent of owner cleanup.
	exec(peer, "CREATE TEMP SEQUENCE peer_temp START 81")
	held, err := c.PrepareContext(ctx, "SELECT nextval('movable'),nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldValue := func(wantTemp, wantPersistent int64) {
		var a, b int64
		if err := held.QueryRowContext(ctx).Scan(&a, &b); err != nil || a != wantTemp || b != wantPersistent {
			t.Fatal("held group", a, b, err)
		}
	}
	heldValue(31, 10)
	exec(c, "CREATE SEQUENCE temp_move_a.s START 100 CACHE 1")
	// This new shadow must not be adopted by the held group after an error.
	state := func(name string, last int64, called bool) int64 {
		var got, log int64
		var isCalled bool
		if err := c.QueryRowContext(ctx, "SELECT last_value,log_cnt,is_called FROM "+name).Scan(&got, &log, &isCalled); err != nil || got != last || isCalled != called || log < 0 {
			t.Fatal(name, got, log, isCalled, err)
		}
		return log
	}
	log := state("pg_temp.movable", 35, true)
	// A retained row is an immutable snapshot, not a new lookup after relocation.
	rows, err := c.QueryContext(ctx, "SELECT last_value,is_called FROM pg_temp.movable")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	readSnapshot := func() {
		columns, err := rows.Columns()
		if err != nil || len(columns) != 2 || columns[0] != "last_value" || columns[1] != "is_called" {
			t.Fatal("snapshot columns", columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "BOOL" {
			t.Fatal("snapshot types", types, err)
		}
		if !rows.Next() {
			t.Fatal("missing snapshot row", rows.Err())
		}
		var frozen int64
		var called bool
		if err := rows.Scan(&frozen, &called); err != nil || frozen != 35 || !called {
			t.Fatal(frozen, called, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("snapshot row count", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// lib/pq permits only one active query on a physical connection. PostgreSQL
	// therefore validates and closes this real snapshot before later commands.
	// Native keeps its actual detached Rows unread until after the rejection.
	if !retainRows {
		readSnapshot()
	}
	for _, q := range []string{
		"ALTER SEQUENCE pg_temp.movable SET SCHEMA public",
		"ALTER SEQUENCE public.persistent_move SET SCHEMA pg_temp",
		"ALTER SEQUENCE pg_temp.movable SET SCHEMA pg_temp",
	} {
		diagnostic(utility(q), "0A000", "cannot move objects into or out of temporary schemas", "", "")
		if after := state("pg_temp.movable", 35, true); after != log {
			t.Fatal("rejection changed log_cnt", log, after)
		}
		state("public.persistent_move", 51, false)
		value(c, "SELECT currval('pg_temp.movable')", 31)
		value(c, "SELECT lastval()", 10)
		value(peer, "SELECT last_value FROM public.movable", 1000)
	}
	// Linked restriction precedes destination lookup and temporary endpoint checks.
	for _, destination := range []string{"temp_move_missing", "pg_temp"} {
		diagnostic(utility("ALTER SEQUENCE public.linked SET SCHEMA "+destination), "0A000", "cannot move an owned sequence into another schema", `Sequence "linked" is linked to table "link_table".`, "")
	}
	diagnostic(utility("ALTER SEQUENCE pg_temp.movable SET SCHEMA temp_move_missing"), "3F000", `schema "temp_move_missing" does not exist`, "", "")
	heldValue(32, 11) // original temporary cache tail and original persistent source
	state("pg_temp.movable", 35, true)
	state("temp_move_a.s", 100, false)
	state("temp_move_b.s", 11, true)
	value(c, "SELECT nextval('s')", 100) // fresh binding sees the shadow
	if retainRows {
		readSnapshot()
	}
	// Ordinary persistent relocation remains legal and preserves its reservation.
	value(c, "SELECT nextval('public.persistent_move')", 51)
	if err := utility("ALTER SEQUENCE public.persistent_move SET SCHEMA temp_move_dst"); err != nil {
		t.Fatal(err)
	}
	state("temp_move_dst.persistent_move", 55, true)
	value(c, "SELECT currval('temp_move_dst.persistent_move')", 51)
	value(c, "SELECT nextval('temp_move_dst.persistent_move')", 52)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	// Close the actual owner driver connection, not just a pool borrower.
	db.SetMaxIdleConns(0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	value(peer, "SELECT nextval('public.movable')", 1000)
	value(peer, "SELECT nextval('peer_temp')", 81)
	var missing int64
	err = peer.QueryRowContext(ctx, "SELECT last_value FROM public.owner_only").Scan(&missing)
	// Exact relation-analysis Position belongs to this SELECT, not the utility.
	diagnostic(err, "42P01", `relation "public.owner_only" does not exist`, "", "24")
}
