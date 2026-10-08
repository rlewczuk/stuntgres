//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func referenceVisibilityPair(t *testing.T, conn *sql.Conn, name, firstPath, secondPath string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	var bag map[int64]bool
	discover := func(path string) int64 {
		t.Helper()
		if _, err := conn.ExecContext(ctx, "SET search_path="+path); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.QueryContext(ctx, "SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='"+name+"'")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := map[int64]bool{}
		var winner int64
		for rows.Next() {
			var id int64
			var visible bool
			if err := rows.Scan(&id, &visible); err != nil {
				t.Fatal(err)
			}
			if id <= 0 || id > 4294967295 {
				t.Fatal(id)
			}
			if _, ok := seen[id]; ok {
				t.Fatal("duplicate candidate", id)
			}
			seen[id] = visible
			if visible {
				if winner != 0 {
					t.Fatal("multiple visible candidates")
				}
				winner = id
			}
		}
		if rows.Err() != nil || len(seen) != 2 || winner == 0 {
			t.Fatal("incomplete candidates", seen, rows.Err())
		}
		if bag != nil {
			for id := range bag {
				if _, ok := seen[id]; !ok {
					t.Fatal("candidate bag changed", bag, seen)
				}
			}
		}
		bag = seen
		return winner
	}
	a, b := discover(firstPath), discover(secondPath)
	if a == b {
		t.Fatal("winner did not reverse")
	}
	return a, b
}

func referenceVisibilityHeldPair(t *testing.T, conn *sql.Conn, a, b int64, paths []string) {
	t.Helper()
	ctx := context.Background()
	held, err := conn.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for i, path := range paths {
		if _, err := conn.ExecContext(ctx, "SET search_path="+path); err != nil {
			t.Fatal(err)
		}
		for _, obj := range []struct {
			id   int64
			want bool
		}{{a, i == 0}, {b, i == 1}} {
			var got bool
			if err := held.QueryRowContext(ctx, obj.id).Scan(&got); err != nil || got != obj.want {
				t.Fatal(path, obj, got, err)
			}
		}
	}
}

func TestReferenceVisibilityInquiryRestoredSameNameReversal(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE SCHEMA pgv_restore_a", "CREATE SCHEMA pgv_restore_b", "CREATE TABLE pgv_restore_a.pgv_restored_pair(id INT)", "CREATE TABLE pgv_restore_b.pgv_restored_pair(id INT)"} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(q, 0))
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		conn, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		a, b := referenceVisibilityPair(t, conn, "pgv_restored_pair", "pgv_restore_a,pgv_restore_b", "pgv_restore_b,pgv_restore_a")
		referenceVisibilityHeldPair(t, conn, a, b, []string{"pgv_restore_a,pgv_restore_b", "pgv_restore_b,pgv_restore_a", "public"})
		conn.Close()
	}
}

func TestReferenceVisibilityInquiryTempPrecedence(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE SEQUENCE public.pgv_temp_pair", 0))
		run(referenceExec("CREATE TEMP SEQUENCE pgv_temp_pair", 0))
		permanent, temp := referenceVisibilityPair(t, conn, "pgv_temp_pair", "public,pg_temp", "public")
		referenceVisibilityHeldPair(t, conn, permanent, temp, []string{"public,pg_temp", "public"})
		// Inquiry never creates a missing TEMP namespace; this session already owns one.
		run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4402)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}

func TestReferenceVisibilityInquiryUserPathRefresh(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		defer func() {
			for _, q := range []string{"RESET ROLE", "SET search_path=public", "DROP SCHEMA pgv_user_a CASCADE", "DROP SCHEMA pgv_user_b CASCADE", "DROP ROLE pgv_user_a", "DROP ROLE pgv_user_b"} {
				if _, err := conn.ExecContext(context.Background(), q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		for _, q := range []string{"CREATE ROLE pgv_user_a", "CREATE ROLE pgv_user_b", "CREATE SCHEMA pgv_user_a AUTHORIZATION pgv_user_a", "CREATE SCHEMA pgv_user_b AUTHORIZATION pgv_user_b", "CREATE TABLE pgv_user_a.pgv_user_pair(id INT)", "CREATE TABLE pgv_user_b.pgv_user_pair(id INT)"} {
			run(referenceExec(q, 0))
		}
		a, b := referenceVisibilityPair(t, conn, "pgv_user_pair", "pgv_user_a,pgv_user_b", "pgv_user_b,pgv_user_a")
		run(referenceExec(`SET search_path="$user",public`, 0))
		held, err := conn.PrepareContext(context.Background(), "SELECT pg_table_is_visible($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, tc := range []struct {
			role   string
			av, bv bool
		}{{"pgv_user_a", true, false}, {"pgv_user_b", false, true}, {"NONE", false, false}} {
			run(referenceExec("SET ROLE "+tc.role, 0))
			for _, obj := range []struct {
				id   int64
				want bool
			}{{a, tc.av}, {b, tc.bv}} {
				run(referenceSelect("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{obj.want}}, obj.id))
				var got bool
				if err := held.QueryRowContext(context.Background(), obj.id).Scan(&got); err != nil || got != obj.want {
					t.Fatal(tc.role, obj, got, err)
				}
			}
		}
	})
}

func TestReferenceVisibilityInquiryForeignTemp(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db      *sql.DB
		rec     *referenceRecorder
		visible bool
	}{{p.memory, p.memoryRecord, false}, {p.postgres, p.postgresRecord, true}} {
		backend.db.SetMaxOpenConns(2)
		owner, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		referenceRunStep(t, p.ctx, owner, backend.rec, referenceExec("CREATE TEMP SEQUENCE pgv_foreign_temp", 0))
		var id int64
		if err := owner.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgv_foreign_temp'").Scan(&id); err != nil || id <= 0 {
			t.Fatal(id, err)
		}
		if _, err := owner.ExecContext(p.ctx, "SET search_path=pg_temp,public"); err != nil {
			t.Fatal(err)
		}
		var namespace string
		if err := owner.QueryRowContext(p.ctx, "SELECT current_schema").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		peer, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		if _, err := peer.ExecContext(p.ctx, "SET search_path="+namespace+",public"); err != nil {
			t.Fatal(err)
		}
		var before string
		if err := peer.QueryRowContext(p.ctx, "SELECT current_schemas(true)").Scan(&before); err != nil {
			t.Fatal(err)
		}
		// Native paths intentionally exclude foreign TEMP; PostgreSQL searches
		// an explicitly named peer namespace when schema USAGE permits it.
		referenceRunStep(t, p.ctx, peer, backend.rec, referenceSelect("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{backend.visible}}, id))
		var after string
		if err := peer.QueryRowContext(p.ctx, "SELECT current_schemas(true)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("visibility allocated missing TEMP", before, after)
		}
		peer.Close()
		owner.Close()
	}
}

func TestReferenceVisibilityInquiryIndexRelations(t *testing.T) {
	p := newReferencePair(t)
	p.memory.SetMaxOpenConns(1)
	p.postgres.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS pgv_index_a,pgv_index_b CASCADE"); err != nil {
			t.Error("index visibility schema cleanup", err)
		}
	})
	for _, q := range []string{
		"CREATE SCHEMA pgv_index_a", "CREATE SCHEMA pgv_index_b",
		"CREATE TABLE pgv_index_a.index_parent(id INT)", "CREATE TABLE pgv_index_b.index_parent(id INT)",
		"CREATE INDEX pgv_index_pair ON pgv_index_a.index_parent(id)", "CREATE INDEX pgv_index_pair ON pgv_index_b.index_parent(id)",
		"CREATE INDEX pgv_index_table ON pgv_index_a.index_parent(id)", "CREATE TABLE pgv_index_b.pgv_index_table(id INT)",
		"CREATE VIEW pgv_index_a.pgv_view_index AS SELECT id FROM pgv_index_a.index_parent", "CREATE INDEX pgv_view_index ON pgv_index_b.index_parent(id)",
		"CREATE SEQUENCE pgv_index_a.pgv_sequence_index", "CREATE INDEX pgv_sequence_index ON pgv_index_b.index_parent(id)",
	} {
		p.step(t, referenceExec(q, 0))
	}
	p.step(t, referenceExec("INSERT INTO pgv_index_a.index_parent VALUES(7)", 1))
	p.step(t, referenceExec("INSERT INTO pgv_index_b.index_parent VALUES(9)", 1))
	check := func() {
		t.Helper()
		for _, name := range []string{"pgv_index_pair", "pgv_index_table", "pgv_view_index", "pgv_sequence_index"} {
			for _, tc := range []struct {
				path   string
				av, bv bool
			}{{"pgv_index_a,pgv_index_b", true, false}, {"pgv_index_b,pgv_index_a", false, true}, {"public", false, false}} {
				p.step(t, referenceExec("SET search_path="+tc.path, 0))
				q := "SELECT pg_table_is_visible('pgv_index_a." + name + "'::regclass),pg_table_is_visible('pgv_index_b." + name + "'::regclass)"
				p.step(t, referenceSelect(q, []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.av, tc.bv}}))
			}
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				conn, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				a, b := referenceVisibilityPair(t, conn, name, "pgv_index_a,pgv_index_b", "pgv_index_b,pgv_index_a")
				referenceVisibilityHeldPair(t, conn, a, b, []string{"pgv_index_a,pgv_index_b", "pgv_index_b,pgv_index_a", "public"})
				conn.Close()
			}
		}
	}
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL retains its independently created objects. Compare native
	// reconstructed identities with the same explicit expectations, not OIDs.
	check()
	p.step(t, referenceSelect("SELECT id FROM pgv_index_a.index_parent", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.step(t, referenceSelect("SELECT id FROM pgv_index_b.index_parent", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
}
