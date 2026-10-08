package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDeparseConstraintsReviewCommentConsumers(t *testing.T) {
	for _, tc := range []struct{ name, provider, compact, pretty, target, identity string }{
		{"object", "obj_description(id,'pg_class')", "obj_description((id)::oid, 'pg_class'::name)", "obj_description(id::oid, 'pg_class'::name)", "TABLE anchor", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"legacy", "obj_description(id)", "obj_description((id)::oid)", "obj_description(id::oid)", "TABLE anchor", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"column", "col_description(id,1)", "col_description((id)::oid, 1)", "col_description(id::oid, 1)", "COLUMN anchor.a", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"namespace", "obj_description(id,'pg_namespace')", "obj_description((id)::oid, 'pg_namespace'::name)", "obj_description(id::oid, 'pg_namespace'::name)", "SCHEMA dc_comment_ns", "SELECT relnamespace FROM pg_catalog.pg_class WHERE relname='dc_comment_anchor'"},
		{"shared", "shobj_description(id,'pg_authid')", "shobj_description((id)::oid, 'pg_authid'::name)", "shobj_description(id::oid, 'pg_authid'::name)", "", "SELECT relowner FROM pg_catalog.pg_class WHERE relname='anchor'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, conn := randomConnection(t)
			_ = db
			if r, e := conn.ExecContext(context.Background(), "CREATE TABLE anchor(a INT)"); e != nil {
				t.Fatal(e)
			} else if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			if tc.name == "namespace" {
				if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA dc_comment_ns"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := conn.ExecContext(context.Background(), "DROP SCHEMA dc_comment_ns CASCADE"); err != nil {
						t.Error(err)
					}
				}()
				if _, err := conn.ExecContext(context.Background(), "CREATE TABLE dc_comment_ns.dc_comment_anchor(a INT)"); err != nil {
					t.Fatal(err)
				}
			}
			var identity int64
			if err := conn.QueryRowContext(context.Background(), tc.identity).Scan(&identity); err != nil {
				t.Fatal(err)
			}
			target := tc.target
			if tc.name == "shared" {
				var owner string
				if err := conn.QueryRowContext(context.Background(), "SELECT CURRENT_USER").Scan(&owner); err != nil {
					t.Fatal(err)
				}
				target = `ROLE "` + strings.ReplaceAll(owner, `"`, `""`) + `"`
			}
			var old sql.NullString
			readProvider := strings.Replace(tc.provider, "id", strconv.FormatInt(identity, 10), 1)
			if err := conn.QueryRowContext(context.Background(), "SELECT "+readProvider).Scan(&old); err != nil {
				t.Fatal(err)
			}
			comment := func(value any) {
				literal := "NULL"
				if value != nil {
					literal = "'" + strings.ReplaceAll(value.(string), "'", "''") + "'"
				}
				if _, err := conn.ExecContext(context.Background(), "COMMENT ON "+target+" IS "+literal); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				if old.Valid {
					comment(old.String)
				} else {
					comment(nil)
				}
			}()
			comment(nil)
			if r, e := conn.ExecContext(context.Background(), "CREATE TABLE original(id INT,expected TEXT,CONSTRAINT c CHECK("+tc.provider+" IS NOT DISTINCT FROM expected))"); e != nil {
				t.Fatal(e)
			} else if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			for mode, provider := range []string{tc.compact, tc.pretty} {
				expr := "(NOT (" + provider + " IS DISTINCT FROM expected))"
				if mode == 1 {
					expr = "NOT " + provider + " IS DISTINCT FROM expected"
				}
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				deparseReviewConnText(t, conn, q, "CHECK ("+expr+")")
				var actual string
				if err := conn.QueryRowContext(context.Background(), q).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				if actual != "CHECK ("+expr+")" {
					t.Fatal(actual, expr)
				}
				if r, e := conn.ExecContext(context.Background(), fmt.Sprintf("CREATE TABLE copy%d(id INT,expected TEXT,CONSTRAINT c%d %s)", mode, mode, actual)); e != nil {
					t.Fatal(e)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				held, err := conn.PrepareContext(context.Background(), "INSERT INTO "+name+" VALUES($1,$2)")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				update, err := conn.PrepareContext(context.Background(), "UPDATE "+name+" SET expected=$1 WHERE id=$2")
				if err != nil {
					t.Fatal(err)
				}
				defer update.Close()
				for _, prepared := range []bool{false, true} {
					comment(nil)
					insert := func(id, value any, reject bool, detail string) {
						var r sql.Result
						var err error
						if prepared {
							r, err = held.ExecContext(context.Background(), id, value)
						} else {
							r, err = conn.ExecContext(context.Background(), "INSERT INTO "+name+" VALUES($1,$2)", id, value)
						}
						if reject {
							if r != nil {
								t.Fatal(r)
							}
							notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
						} else if err != nil {
							t.Fatal(err)
						}
					}
					insert(identity, nil, false, "")
					insert(int64(0), nil, false, "")
					insert(nil, nil, false, "")
					insert(identity, "old", true, strconv.FormatInt(identity, 10)+", old")
					insert(int64(0), "old", true, "0, old")
					insert(nil, "old", true, "null, old")
					deparseReviewCommentRows(t, conn, name, [][]any{{nil, nil}, {int64(0), nil}, {identity, nil}})
					if r, e := conn.ExecContext(context.Background(), "DELETE FROM "+name); e != nil {
						t.Fatal(e)
					} else if n, _ := r.RowsAffected(); n != 3 {
						t.Fatal(n)
					}
					comment("old")
					var read string
					if err := conn.QueryRowContext(context.Background(), "SELECT "+readProvider).Scan(&read); err != nil || read != "old" {
						t.Fatal(read, err)
					}
					insert(identity, "old", false, "")
					insert(identity, "old", false, "")
					insert(identity, nil, true, strconv.FormatInt(identity, 10)+", null")
					insert(identity, "new", true, strconv.FormatInt(identity, 10)+", new")
					idSQL := strconv.FormatInt(identity, 10)
					query := "INSERT INTO " + name + " VALUES(" + idSQL + ",'old'),(" + idSQL + ",'new'),(" + idSQL + ",'old')"
					var err error
					if prepared {
						s, e := conn.PrepareContext(context.Background(), query)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.ExecContext(context.Background())
						s.Close()
					} else {
						_, err = conn.ExecContext(context.Background(), query)
					}
					detail := idSQL + ", new"
					notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
					deparseReviewCommentRows(t, conn, name, [][]any{{identity, "old"}, {identity, "old"}})
					if prepared {
						_, err = update.ExecContext(context.Background(), "new", identity)
					} else {
						_, err = conn.ExecContext(context.Background(), "UPDATE "+name+" SET expected='new' WHERE id="+idSQL)
					}
					notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
					deparseReviewCommentRows(t, conn, name, [][]any{{identity, "old"}, {identity, "old"}})
					comment("new")
					if err := conn.QueryRowContext(context.Background(), "SELECT "+readProvider).Scan(&read); err != nil || read != "new" {
						t.Fatal(read, err)
					}
					// The SAME held statements must recapture provider state, not prepare-time comments.
					if _, err := held.ExecContext(context.Background(), identity, "new"); err != nil {
						t.Fatal(err)
					}
					if _, err := update.ExecContext(context.Background(), "new", identity); err != nil {
						t.Fatal(err)
					}
					insert(identity, "old", true, idSQL+", old")
					deparseReviewCommentRows(t, conn, name, [][]any{{identity, "new"}, {identity, "new"}, {identity, "new"}})
					comment(nil)
					var cleared sql.NullString
					if err := conn.QueryRowContext(context.Background(), "SELECT "+readProvider).Scan(&cleared); err != nil || cleared.Valid {
						t.Fatal(cleared, err)
					}
					if _, err := update.ExecContext(context.Background(), nil, identity); err != nil {
						t.Fatal(err)
					}
					insert(identity, "new", true, idSQL+", new")
					deparseReviewCommentRows(t, conn, name, [][]any{{identity, nil}, {identity, nil}, {identity, nil}})
					if r, e := conn.ExecContext(context.Background(), "DELETE FROM "+name); e != nil {
						t.Fatal(e)
					} else if n, _ := r.RowsAffected(); n != 3 {
						t.Fatal(n)
					}
				}
				held.Close()
				update.Close()
			}

		})
	}
}

func deparseReviewCommentRows(t *testing.T, conn *sql.Conn, name string, want [][]any) {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, tuple := range want {
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var id, expected any
		if err := rows.Scan(&id, &expected); err != nil || !reflect.DeepEqual([]any{id, expected}, tuple) {
			t.Fatal(id, expected, tuple, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
}
