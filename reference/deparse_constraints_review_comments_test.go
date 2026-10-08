//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceDeparseConstraintsReviewCommentConsumers(t *testing.T) {
	for _, tc := range []struct{ name, provider, compact, pretty, target, identity string }{
		{"object", "obj_description(id,'pg_class')", "obj_description((id)::oid, 'pg_class'::name)", "obj_description(id::oid, 'pg_class'::name)", "TABLE anchor", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"legacy", "obj_description(id)", "obj_description((id)::oid)", "obj_description(id::oid)", "TABLE anchor", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"column", "col_description(id,1)", "col_description((id)::oid, 1)", "col_description(id::oid, 1)", "COLUMN anchor.a", "SELECT oid FROM pg_catalog.pg_class WHERE relname='anchor'"},
		{"namespace", "obj_description(id,'pg_namespace')", "obj_description((id)::oid, 'pg_namespace'::name)", "obj_description(id::oid, 'pg_namespace'::name)", "SCHEMA dc_comment_ns", "SELECT relnamespace FROM pg_catalog.pg_class WHERE relname='dc_comment_anchor'"},
		{"shared", "shobj_description(id,'pg_authid')", "shobj_description((id)::oid, 'pg_authid'::name)", "shobj_description(id::oid, 'pg_authid'::name)", "ROLE postgres", "SELECT relowner FROM pg_catalog.pg_class WHERE relname='anchor'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("CREATE TABLE anchor(a INT)", 0))
				if tc.name == "namespace" {
					run(referenceExec("CREATE SCHEMA dc_comment_ns", 0))
					defer func() {
						if _, err := conn.ExecContext(context.Background(), "DROP SCHEMA dc_comment_ns CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					run(referenceExec("CREATE TABLE dc_comment_ns.dc_comment_anchor(a INT)", 0))
				}
				var identity int64
				if err := conn.QueryRowContext(context.Background(), tc.identity).Scan(&identity); err != nil {
					t.Fatal(err)
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
					if _, err := conn.ExecContext(context.Background(), "COMMENT ON "+tc.target+" IS "+literal); err != nil {
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
				run(referenceExec("CREATE TABLE original(id INT,expected TEXT,CONSTRAINT c CHECK("+tc.provider+" IS NOT DISTINCT FROM expected))", 0))
				for mode, provider := range []string{tc.compact, tc.pretty} {
					expr := "(NOT (" + provider + " IS DISTINCT FROM expected))"
					if mode == 1 {
						expr = "NOT " + provider + " IS DISTINCT FROM expected"
					}
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,expected TEXT,CONSTRAINT c%d %s)", mode, mode, actual), 0))
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
								{
									want := referenceCheckViolation(name, constraint, detail)
									want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
									referenceAssertDiagnostic(t, err, &want)
								}
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
						run(referenceSelect("SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST", []string{"id", "expected"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}, {int64(0), nil}, {identity, nil}}))
						run(referenceExec("DELETE FROM "+name, 3))
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
						{
							want := referenceCheckViolation(name, constraint, detail)
							want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
							referenceAssertDiagnostic(t, err, &want)
						}
						run(referenceSelect("SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST", []string{"id", "expected"}, []string{"INT4", "TEXT"}, [][]any{{identity, "old"}, {identity, "old"}}))
						if prepared {
							_, err = update.ExecContext(context.Background(), "new", identity)
						} else {
							_, err = conn.ExecContext(context.Background(), "UPDATE "+name+" SET expected='new' WHERE id="+idSQL)
						}
						{
							want := referenceCheckViolation(name, constraint, detail)
							want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
							referenceAssertDiagnostic(t, err, &want)
						}
						run(referenceSelect("SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST", []string{"id", "expected"}, []string{"INT4", "TEXT"}, [][]any{{identity, "old"}, {identity, "old"}}))
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
						run(referenceSelect("SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST", []string{"id", "expected"}, []string{"INT4", "TEXT"}, [][]any{{identity, "new"}, {identity, "new"}, {identity, "new"}}))
						comment(nil)
						var cleared sql.NullString
						if err := conn.QueryRowContext(context.Background(), "SELECT "+readProvider).Scan(&cleared); err != nil || cleared.Valid {
							t.Fatal(cleared, err)
						}
						if _, err := update.ExecContext(context.Background(), nil, identity); err != nil {
							t.Fatal(err)
						}
						insert(identity, "new", true, idSQL+", new")
						run(referenceSelect("SELECT id,expected FROM "+name+" ORDER BY id NULLS FIRST", []string{"id", "expected"}, []string{"INT4", "TEXT"}, [][]any{{identity, nil}, {identity, nil}, {identity, nil}}))
						run(referenceExec("DELETE FROM "+name, 3))
					}
					held.Close()
					update.Close()
				}
			})
		})
	}
}
