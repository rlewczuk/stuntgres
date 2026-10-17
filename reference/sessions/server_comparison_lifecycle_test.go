//go:build !noservertests

package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// Source expectations: PostgreSQL18 protocol-flow (fixed source parameters,
// fresh Bind frames), PREPARE and GRANT; comparison selection must not change
// the existing authorization/reanalysis owner or its failure precedence.
func TestReferenceServerComparisonLifecycle(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, query := range []string{serverComparisonCount, "SELECT COUNT(*) FROM pg_namespace WHERE nspname=$1::varchar"} {
				held, err := c.PrepareContext(f.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range []struct {
					arg  any
					want int64
				}{{"public", 1}, {"comparison_public_absent_186", 0}, {nil, 0}, {"public", 1}} {
					serverComparisonPublicCount(t, f.Ctx, c, held, query, v.arg, v.want)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = c.ExecContext(f.Ctx, "PREPARE comparison_public_count(varchar) AS "+serverComparisonCount); err != nil {
				t.Fatal(err)
			}
			for _, v := range []struct {
				query string
				want  int64
			}{{"EXECUTE comparison_public_count('public')", 1}, {"EXECUTE comparison_public_count('comparison_public_absent_186')", 0}, {"EXECUTE comparison_public_count(NULL)", 0}, {"EXECUTE comparison_public_count('public')", 1}} {
				var got int64
				if err = c.QueryRowContext(f.Ctx, v.query).Scan(&got); err != nil || got != v.want {
					t.Fatalf("SQL PREPARE got %d want %d: %v", got, v.want, err)
				}
			}
			if _, err = c.ExecContext(f.Ctx, "DEALLOCATE comparison_public_count"); err != nil {
				t.Fatal(err)
			}
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE TABLE comparison_acl(v name); INSERT INTO comparison_acl VALUES ('public'); CREATE ROLE comparison_wire_reader; GRANT USAGE ON SCHEMA public TO comparison_wire_reader; GRANT SELECT ON comparison_acl TO comparison_wire_reader"), "")
			defer func() {
				serverWantCode(t, w.query(t, "RESET ROLE; DROP TABLE comparison_acl; REVOKE USAGE ON SCHEMA public FROM comparison_wire_reader; DROP ROLE comparison_wire_reader"), "")
			}()
			reader := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, reader.query(t, "SET ROLE comparison_wire_reader"), "")
			const query = "SELECT v=$1 FROM comparison_acl"
			serverComparisonDescription(t, serverCycle(t, reader, serverParse("acl_compare", query, 1043), serverDescribe('S', "acl_compare")), []uint32{1043}, []uint32{16})
			serverWantRaw(t, serverCycle(t, reader, serverBind("", "acl_compare", nil, [][]byte{[]byte("public")}), serverExecute("", 0)), [][][]byte{{[]byte("t")}})
			serverWantCode(t, w.query(t, "REVOKE SELECT ON comparison_acl FROM comparison_wire_reader"), "")
			denied := serverCycle(t, reader, serverBind("denied", "acl_compare", nil, [][]byte{[]byte("public")}), serverExecute("denied", 0))
			serverComparisonError(t, denied, "42501", "permission denied for table comparison_acl", "", "")
			serverNotificationError(t, serverCycle(t, reader, serverExecute("denied", 0)), "34000", "portal \"denied\" does not exist", "")
			serverWantCode(t, w.query(t, "GRANT SELECT ON comparison_acl TO comparison_wire_reader"), "")
			serverWantRaw(t, serverCycle(t, reader, serverBind("", "acl_compare", nil, [][]byte{[]byte("public")}), serverExecute("", 0)), [][][]byte{{[]byte("t")}})
			serverComparisonDescription(t, serverCycle(t, reader, serverDescribe('S', "acl_compare")), []uint32{1043}, []uint32{16})
			serverWantCode(t, reader.query(t, "RESET ROLE"), "")
			// Parse fails before malformed Bind input is considered, without publishing
			// the statement. Input failure on an admitted statement is a separate stage.
			const bad = "SELECT $1 = $2"
			failed := serverCycle(t, w, serverParse("precedence", bad, 19, 16), serverBind("", "precedence", nil, [][]byte{[]byte("public"), []byte("not-a-bool")}), serverExecute("", 0))
			serverComparisonError(t, failed, "42883", "operator does not exist: name = boolean", strconv.Itoa(11), "No operator matches the given name and argument types. You might need to add explicit type casts.")
			serverNotificationError(t, serverCycle(t, w, serverDescribe('S', "precedence")), "26000", "prepared statement \"precedence\" does not exist", "")
			serverWantCode(t, serverCycle(t, w, serverParse("input_compare", "SELECT $1 < $2", 23, 23)), "")
			serverNotificationError(t, serverCycle(t, w, serverBind("bad_input", "input_compare", nil, [][]byte{[]byte("bad"), []byte("2")}), serverExecute("bad_input", 0)), "22P02", "invalid input syntax for type integer: \"bad\"", "", "portal \"bad_input\" parameter $1 = '...'")
			serverNotificationError(t, serverCycle(t, w, serverExecute("bad_input", 0)), "34000", "portal \"bad_input\" does not exist", "")
			serverWantRaw(t, serverCycle(t, w, serverBind("", "input_compare", nil, [][]byte{[]byte("1"), []byte("2")}), serverExecute("", 0)), [][][]byte{{[]byte("t")}})
		})
	}
}

func serverComparisonPublicCount(t *testing.T, ctx context.Context, c *sql.Conn, s *sql.Stmt, query string, arg any, want int64) {
	t.Helper()
	for _, held := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if held {
			rows, err = s.QueryContext(ctx, arg)
		} else {
			rows, err = c.QueryContext(ctx, query, arg)
		}
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if len(types) != 1 || types[0].Name() != "count" || types[0].DatabaseTypeName() != "INT8" {
			rows.Close()
			t.Fatalf("count metadata %v", types)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing count row")
		}
		var got int64
		if err = rows.Scan(&got); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if got != want {
			rows.Close()
			t.Fatalf("count %d want %d", got, want)
		}
		more := rows.Next()
		err = rows.Err()
		closeErr := rows.Close()
		if more || err != nil || closeErr != nil {
			t.Fatalf("count completion extra=%v err=%v close=%v", more, err, closeErr)
		}
	}
}
