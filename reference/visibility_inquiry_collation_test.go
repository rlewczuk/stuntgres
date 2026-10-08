//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func visibilityOracleTransaction(t *testing.T, p *referencePair) *sql.Tx {
	t.Helper()
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	tx, err := conn.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Error(err)
		}
	})
	return tx
}

func TestReferenceVisibilityInquiryCollationEncoding(t *testing.T) {
	p := newReferencePair(t)
	for _, scenario := range []string{"NamespaceBeforeEncoding", "IneligibleExisting", "ExactBeforeFallback"} {
		t.Run(scenario, func(t *testing.T) {
			tx := visibilityOracleTransaction(t, p)
			exec := func(q string, count int64) {
				t.Helper()
				r, err := tx.ExecContext(p.ctx, q)
				if err != nil {
					t.Fatal(q, err)
				}
				n, err := r.RowsAffected()
				if err != nil || n != count {
					t.Fatal(q, n, count, err)
				}
			}
			run := func(q string, want [][]any) {
				t.Helper()
				referenceRunStep(t, p.ctx, tx, p.postgresRecord, referenceSelect(q, []string{"schema_name", "collencoding", "visible"}, []string{"TEXT", "INT4", "BOOL"}, want))
			}
			query := "SELECT n.nspname::text AS schema_name,c.collencoding,pg_catalog.pg_collation_is_visible(c.oid) AS visible FROM pg_catalog.pg_collation c JOIN pg_catalog.pg_namespace n ON n.oid=c.collnamespace WHERE n.nspname IN ('pgv_col_a','pgv_col_b') AND c.collname='same' ORDER BY n.nspname,c.collencoding"
			if scenario != "ExactBeforeFallback" {
				exec("CREATE SCHEMA pgv_col_a", 0)
				exec("CREATE SCHEMA pgv_col_b", 0)
				exec("CREATE COLLATION pgv_col_a.same FROM pg_catalog.\"C\"", 0)
				copy := "pg_catalog.pg_c_utf8"
				if scenario == "IneligibleExisting" {
					copy = "pg_catalog.\"C\""
				}
				exec("CREATE COLLATION pgv_col_b.same FROM "+copy, 0)
				var a, b int64
				if err := tx.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_collation WHERE collnamespace='pgv_col_a'::regnamespace AND collname='same'").Scan(&a); err != nil {
					t.Fatal(err)
				}
				if err := tx.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_collation WHERE collnamespace='pgv_col_b'::regnamespace AND collname='same'").Scan(&b); err != nil {
					t.Fatal(err)
				}
				stmt, err := tx.PrepareContext(p.ctx, "SELECT pg_catalog.pg_collation_is_visible($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				checkHeld := func(wa, wb bool) {
					t.Helper()
					for _, object := range []struct {
						id   int64
						want bool
					}{{a, wa}, {b, wb}} {
						var got bool
						if err := stmt.QueryRowContext(p.ctx, object.id).Scan(&got); err != nil || got != object.want {
							t.Fatal(got, object.want, err)
						}
					}
				}
				if scenario == "NamespaceBeforeEncoding" {
					exec("SET LOCAL search_path=pgv_col_a,pgv_col_b,pg_catalog", 0)
					run(query, [][]any{{"pgv_col_a", int64(-1), true}, {"pgv_col_b", int64(6), false}})
					checkHeld(true, false)
					exec("SET LOCAL search_path=pgv_col_b,pgv_col_a,pg_catalog", 0)
					run(query, [][]any{{"pgv_col_a", int64(-1), false}, {"pgv_col_b", int64(6), true}})
					checkHeld(false, true)
				} else {
					exec("UPDATE pg_catalog.pg_collation SET collencoding=pg_catalog.pg_char_to_encoding('LATIN1') WHERE collnamespace='pgv_col_a'::regnamespace AND collname='same' AND collencoding=-1", 1)
					exec("SET LOCAL search_path=pgv_col_a,pgv_col_b,pg_catalog", 0)
					run(query, [][]any{{"pgv_col_a", int64(8), false}, {"pgv_col_b", int64(-1), true}})
					checkHeld(false, true)
					exec("SET LOCAL search_path=pgv_col_a,pg_catalog", 0)
					checkHeld(false, false)
					referenceRunStep(t, p.ctx, tx, p.postgresRecord, referenceSelect("SELECT pg_catalog.pg_collation_is_visible($1),pg_catalog.pg_collation_is_visible($1) IS NULL", []string{"pg_collation_is_visible", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}, a))
				}
				return
			}
			exec("CREATE SCHEMA pgv_col_exact", 0)
			for _, name := range []string{"same", "exact_seed", "wrong_seed"} {
				exec("CREATE COLLATION pgv_col_exact."+name+" FROM pg_catalog.\"C\"", 0)
			}
			var fallback, exact, wrong int64
			for _, object := range []struct {
				name string
				id   *int64
			}{{"same", &fallback}, {"exact_seed", &exact}, {"wrong_seed", &wrong}} {
				if err := tx.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_collation WHERE collnamespace='pgv_col_exact'::regnamespace AND collname=$1", object.name).Scan(object.id); err != nil {
					t.Fatal(err)
				}
			}
			stmt, err := tx.PrepareContext(p.ctx, "SELECT pg_catalog.pg_collation_is_visible($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			checkHeld := func(wf, we, ww bool) {
				t.Helper()
				for _, object := range []struct {
					id   int64
					want bool
				}{{fallback, wf}, {exact, we}, {wrong, ww}} {
					var got bool
					if err := stmt.QueryRowContext(p.ctx, object.id).Scan(&got); err != nil || got != object.want {
						t.Fatal(got, object.want, err)
					}
				}
			}
			exec("UPDATE pg_catalog.pg_collation SET collname='same',collencoding=pg_catalog.pg_char_to_encoding(pg_catalog.current_setting('server_encoding')) WHERE collnamespace='pgv_col_exact'::regnamespace AND collname='exact_seed' AND collencoding=-1", 1)
			exec("UPDATE pg_catalog.pg_collation SET collname='same',collencoding=pg_catalog.pg_char_to_encoding('LATIN1') WHERE collnamespace='pgv_col_exact'::regnamespace AND collname='wrong_seed' AND collencoding=-1", 1)
			exec("SET LOCAL search_path=pgv_col_exact,pg_catalog", 0)
			query = "SELECT n.nspname::text AS schema_name,c.collencoding,pg_catalog.pg_collation_is_visible(c.oid) AS visible FROM pg_catalog.pg_collation c JOIN pg_catalog.pg_namespace n ON n.oid=c.collnamespace WHERE n.nspname='pgv_col_exact' AND c.collname='same' ORDER BY c.collencoding"
			run(query, [][]any{{"pgv_col_exact", int64(-1), false}, {"pgv_col_exact", int64(6), true}, {"pgv_col_exact", int64(8), false}})
			checkHeld(false, true, false)
			exec("UPDATE pg_catalog.pg_collation SET collencoding=pg_catalog.pg_char_to_encoding('LATIN2') WHERE collnamespace='pgv_col_exact'::regnamespace AND collname='same' AND collencoding=pg_catalog.pg_char_to_encoding(pg_catalog.current_setting('server_encoding'))", 1)
			run(query, [][]any{{"pgv_col_exact", int64(-1), true}, {"pgv_col_exact", int64(8), false}, {"pgv_col_exact", int64(9), false}})
			checkHeld(true, false, false)
			exec("SET LOCAL search_path=pg_catalog", 0)
			run(query, [][]any{{"pgv_col_exact", int64(-1), false}, {"pgv_col_exact", int64(8), false}, {"pgv_col_exact", int64(9), false}})
			checkHeld(false, false, false)
		})
	}
}
