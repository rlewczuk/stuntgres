package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

// Discover both rows, including the shadowed candidate, without imposing scan order.
func visibilityCandidatePair(t *testing.T, conn *sql.Conn, name, a, b string) (int64, int64) {
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
				t.Fatal("duplicate identity", id)
			}
			seen[id] = visible
			if visible {
				if winner != 0 {
					t.Fatal("multiple winners")
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
					t.Fatal("unstable identity bag", bag, seen)
				}
			}
		}
		bag = seen
		return winner
	}
	first, second := discover(a), discover(b)
	if first == second {
		t.Fatal("same winner")
	}
	return first, second
}

func TestVisibilityInquiryRestoredSameNameReversal(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.restored_pair(id INT)", "CREATE TABLE b.restored_pair(id INT)"} {
		execSQL(t, db, q)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a, b := visibilityCandidatePair(t, conn, "restored_pair", "a,b", "b,a")
	held, err := conn.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		path   string
		av, bv bool
	}{{"a,b", true, false}, {"b,a", false, true}, {"public", false, false}} {
		if _, err := conn.ExecContext(ctx, "SET search_path="+tc.path); err != nil {
			t.Fatal(err)
		}
		for _, obj := range []struct {
			id   int64
			want bool
		}{{a, tc.av}, {b, tc.bv}} {
			var got bool
			if err := held.QueryRowContext(ctx, obj.id).Scan(&got); err != nil || got != obj.want {
				t.Fatal(tc.path, obj, got, err)
			}
		}
	}
}

func TestVisibilityInquiryUserPathRefresh(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE ROLE pgv_user_a", "CREATE ROLE pgv_user_b", "CREATE SCHEMA pgv_user_a AUTHORIZATION pgv_user_a", "CREATE SCHEMA pgv_user_b AUTHORIZATION pgv_user_b", "CREATE TABLE pgv_user_a.user_pair(id INT)", "CREATE TABLE pgv_user_b.user_pair(id INT)"} {
		execSQL(t, db, q)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a, b := visibilityCandidatePair(t, conn, "user_pair", "pgv_user_a,pgv_user_b", "pgv_user_b,pgv_user_a")
	if _, err := conn.ExecContext(ctx, `SET search_path="$user",public`); err != nil {
		t.Fatal(err)
	}
	held, err := conn.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		role   string
		av, bv bool
	}{{"pgv_user_a", true, false}, {"pgv_user_b", false, true}, {"NONE", false, false}} {
		if _, err := conn.ExecContext(ctx, "SET ROLE "+tc.role); err != nil {
			t.Fatal(err)
		}
		for _, obj := range []struct {
			id   int64
			want bool
		}{{a, tc.av}, {b, tc.bv}} {
			var got bool
			if err := held.QueryRowContext(ctx, obj.id).Scan(&got); err != nil || got != obj.want {
				t.Fatal(tc.role, obj, got, err)
			}
		}
	}
}

func TestVisibilityInquiryTempPrecedence(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	owner, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, q := range []string{"CREATE SEQUENCE public.temp_pair", "CREATE TEMP SEQUENCE temp_pair"} {
		if _, err := owner.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	permanent, temp := visibilityCandidatePair(t, owner, "temp_pair", "public,pg_temp", "public")
	held, err := owner.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		path string
		p, t bool
	}{{"public,pg_temp", true, false}, {"public", false, true}} {
		if _, err := owner.ExecContext(ctx, "SET search_path="+tc.path); err != nil {
			t.Fatal(err)
		}
		for _, obj := range []struct {
			id   int64
			want bool
		}{{permanent, tc.p}, {temp, tc.t}} {
			var got bool
			if err := held.QueryRowContext(ctx, obj.id).Scan(&got); err != nil || got != obj.want {
				t.Fatal(tc.path, obj, got, err)
			}
		}
	}
	if _, err := owner.ExecContext(ctx, "SET search_path=pg_temp,public"); err != nil {
		t.Fatal(err)
	}
	var namespace string
	if err := owner.QueryRowContext(ctx, "SELECT current_schema").Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := peer.ExecContext(ctx, "SET search_path="+namespace+",public"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := peer.QueryRowContext(ctx, "SELECT current_schemas(true)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	var visible bool
	if err := peer.QueryRowContext(ctx, "SELECT pg_table_is_visible($1)", temp).Scan(&visible); err != nil || visible {
		t.Fatal("foreign TEMP became visible", visible, err)
	}
	var after string
	if err := peer.QueryRowContext(ctx, "SELECT current_schemas(true)").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("visibility allocated missing TEMP", before, after)
	}
}

// Indexes compete with every supported relation kind, not just other indexes.
// Rediscover identities after Restore: generated OIDs are not portable handles.
func TestVisibilityInquiryIndexRelations(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE SCHEMA a", "CREATE SCHEMA b",
		"CREATE TABLE a.index_parent(id INT)", "CREATE TABLE b.index_parent(id INT)",
		"INSERT INTO a.index_parent VALUES(7)", "INSERT INTO b.index_parent VALUES(9)",
		"CREATE INDEX vi_index_pair ON a.index_parent(id)", "CREATE INDEX vi_index_pair ON b.index_parent(id)",
		"CREATE INDEX vi_index_table ON a.index_parent(id)", "CREATE TABLE b.vi_index_table(id INT)",
		"CREATE VIEW a.vi_view_index AS SELECT id FROM a.index_parent", "CREATE INDEX vi_view_index ON b.index_parent(id)",
		"CREATE SEQUENCE a.vi_sequence_index", "CREATE INDEX vi_sequence_index ON b.index_parent(id)",
	} {
		execSQL(t, db, q)
	}
	check := func() {
		t.Helper()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		held, err := conn.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, name := range []string{"vi_index_pair", "vi_index_table", "vi_view_index", "vi_sequence_index"} {
			a, b := visibilityCandidatePair(t, conn, name, "a,b", "b,a")
			for _, tc := range []struct {
				path   string
				av, bv bool
			}{{"a,b", true, false}, {"b,a", false, true}, {"public", false, false}} {
				if _, err := conn.ExecContext(ctx, "SET search_path="+tc.path); err != nil {
					t.Fatal(err)
				}
				// Qualified identities specify the expected winner independently
				// of the discovery inquiry used to map the complete candidate bag.
				var av, bv bool
				q := "SELECT pg_table_is_visible('a." + name + "'::regclass),pg_table_is_visible('b." + name + "'::regclass)"
				if err := conn.QueryRowContext(ctx, q).Scan(&av, &bv); err != nil || av != tc.av || bv != tc.bv {
					t.Fatal(name, tc.path, av, bv, err)
				}
				for _, obj := range []struct {
					id   int64
					want bool
				}{{a, tc.av}, {b, tc.bv}} {
					rows, err := held.QueryContext(ctx, obj.id)
					visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{obj.want})
				}
			}
		}
	}
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
	wantTuples(t, db, "SELECT id FROM a.index_parent", [][]any{{int64(7)}})
	wantTuples(t, db, "SELECT id FROM b.index_parent", [][]any{{int64(9)}})
}
