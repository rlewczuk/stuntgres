package stuntgres

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestRoleRetainedIdentityRowsAcrossSwitchAndControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			ctx := context.Background()
			d := NewDriver()
			db := rolePool(t, d, "retained identity rows", "postgres")
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, query := range []string{"CREATE USER original", "CREATE USER later", "SET SESSION AUTHORIZATION original", "CREATE TABLE sentinel(id INT)", "INSERT INTO sentinel VALUES(7)"} {
				count := int64(0)
				if query == "INSERT INTO sentinel VALUES(7)" {
					count = 1
				}
				rolePublicCommand(t, c, query, count, prepared)
			}
			peer, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			rolePublicCommand(t, peer, "SET application_name TO 'peer-retained'", 0, false)
			rolePublicCommand(t, c, "SET application_name TO 'producer-retained'", 0, false)
			checkSessions := func() {
				t.Helper()
				rolePublicIdentity(t, peer, "postgres", nil)
				for _, tc := range []struct {
					conn *sql.Conn
					want string
				}{{peer, "peer-retained"}, {c, "producer-retained"}} {
					var value string
					if err := tc.conn.QueryRowContext(ctx, "SHOW application_name").Scan(&value); err != nil || value != tc.want {
						t.Fatal("settings changed", value, err)
					}
				}
			}
			var held *sql.Stmt
			if prepared {
				held, err = c.PrepareContext(ctx, "SELECT session_user,current_user")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			capture := func() *sql.Rows {
				t.Helper()
				var rows *sql.Rows
				var err error
				if held == nil {
					rows, err = c.QueryContext(ctx, "SELECT session_user,current_user")
				} else {
					rows, err = held.QueryContext(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				return rows
			}
			check := func(rows *sql.Rows, want string) {
				t.Helper()
				defer rows.Close()
				columns, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(columns, []string{"session_user", "current_user"}) {
					t.Fatal(columns, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 {
					t.Fatal(types, err)
				}
				for _, typ := range types {
					if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != reflect.TypeFor[string]() {
						t.Fatal("retained NAME metadata", typ)
					}
				}
				if !rows.Next() {
					t.Fatal("retained row missing", rows.Err())
				}
				var session, current string
				if err := rows.Scan(&session, &current); err != nil || session != want || current != want {
					t.Fatal("retained identity changed", session, current, want, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("retained extra/error", rows.Err())
				}
			}
			var dump bytes.Buffer
			if err := Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			// Separate snapshots witness each boundary; the final one spans all
			// transitions before its first Next/Scan, not just a copied Go value.
			all := capture()
			defer all.Close()
			atSwitch := capture()
			defer atSwitch.Close()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
			check(atSwitch, "original")
			check(capture(), "later")
			checkSessions()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
			atReset := capture()
			defer atReset.Close()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
			if err := Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			check(atReset, "original")
			check(capture(), "later")
			checkSessions()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
			atRestore := capture()
			defer atRestore.Close()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
			if err := Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(atRestore, "original")
			check(all, "original")
			check(capture(), "later")
			checkSessions()
			rolePublicCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
			var value int64
			if err := c.QueryRowContext(ctx, "SELECT id FROM sentinel").Scan(&value); err != nil || value != 7 {
				t.Fatal("restored sentinel", value, err)
			}
			rolePublicCommand(t, c, "RESET SESSION AUTHORIZATION", 0, prepared)
			check(capture(), "postgres")
			checkSessions()
		})
	}
}

func TestRoleTransactionMutationBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"CREATE ROLE tx_created", "DROP ROLE tx_retained", "SET SESSION AUTHORIZATION tx_reader", "RESET SESSION AUTHORIZATION"} {
			t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+query, func(t *testing.T) {
				ctx := context.Background()
				db := rolePool(t, NewDriver(), "role transaction boundary", "postgres")
				c, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				rolePublicCommand(t, c, "CREATE ROLE tx_retained", 0, false)
				rolePublicCommand(t, c, "CREATE USER tx_reader", 0, false)
				rolePublicCommand(t, c, "CREATE TABLE tx_sentinel(id INT)", 0, false)
				rolePublicCommand(t, c, "INSERT INTO tx_sentinel VALUES(7)", 1, false)
				tx, err := c.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				var sessionUser, currentUser string
				if err := tx.QueryRowContext(ctx, "SELECT session_user,current_user").Scan(&sessionUser, &currentUser); err != nil || sessionUser != "postgres" || currentUser != "postgres" {
					t.Fatal("transaction identity", sessionUser, currentUser, err)
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO tx_sentinel VALUES(8)"); err != nil {
					t.Fatal(err)
				}
				if prepared {
					stmt, prepareErr := tx.PrepareContext(ctx, query)
					if stmt != nil {
						stmt.Close()
					}
					err = prepareErr
				} else {
					_, err = tx.ExecContext(ctx, query)
				}
				rolePublicDiagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction")
				_, err = tx.ExecContext(ctx, "INSERT INTO tx_sentinel VALUES(9)")
				rolePublicDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				rolePublicIdentity(t, c, "postgres", nil)
				var count int
				if err := c.QueryRowContext(ctx, "SELECT id FROM tx_sentinel").Scan(&count); err != nil || count != 7 {
					t.Fatal("aborted transaction changed sentinel", count, err)
				}
				rolePublicCommand(t, c, "CREATE ROLE tx_created", 0, false)
				rolePublicCommand(t, c, "DROP ROLE tx_retained,tx_created", 0, false)
			})
		}
	}
}
