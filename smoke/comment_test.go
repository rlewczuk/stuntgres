package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestCommentSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(4)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	exec := func(c *sql.Conn, q string, prepared bool) {
		t.Helper()
		if err := transactionAffected(ctx, c, q, prepared, 0); err != nil {
			t.Fatal(q, err)
		}
	}
	dump := func() string {
		t.Helper()
		var buffer bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &buffer); err != nil {
			t.Fatal(err)
		}
		return buffer.String()
	}
	diagnostic := func(err error, code, message, detail string) {
		t.Helper()
		if err := transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}); err != nil {
			t.Fatal(err)
		}
	}
	var canonical string
	t.Run("roundtrip", func(t *testing.T) {
		exec(a, `CREATE SCHEMA "s.dot"`, false)
		exec(a, `CREATE TABLE "s.dot"."t.dot"(a INT,b TEXT)`, false)
		if _, err := a.ExecContext(ctx, `INSERT INTO "s.dot"."t.dot" VALUES(7,'sentinel')`); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{`COMMENT ON SCHEMA "s.dot" IS 'schema'`, `COMMENT ON SCHEMA public IS 'bootstrap'`, `COMMENT ON TABLE "s.dot"."t.dot" IS E'quote\' slash\\\n雪'`} {
			exec(a, q, false)
		}
		for _, q := range []string{`COMMENT ON COLUMN "s.dot"."t.dot".a IS 'first'`, `COMMENT ON COLUMN "s.dot"."t.dot".b IS 'second'`} {
			exec(a, q, true)
		}
		rows, err := a.QueryContext(ctx, "COMMENT ON SCHEMA public IS 'bootstrap'")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil || len(columns) != 0 || rows.Next() {
			rows.Close()
			t.Fatal("COMMENT exposed result columns/rows", columns, err)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		canonical = dump()
		for _, line := range []string{`COMMENT ON SCHEMA "public" IS E'bootstrap';`, `COMMENT ON SCHEMA "s.dot" IS E'schema';`, `COMMENT ON TABLE "s.dot"."t.dot" IS E'quote'' slash\\` + "\n雪';", `COMMENT ON COLUMN "s.dot"."t.dot"."a" IS E'first';`, `COMMENT ON COLUMN "s.dot"."t.dot"."b" IS E'second';`} {
			if !strings.Contains(canonical, line) {
				t.Fatal("missing independent payload", line, canonical)
			}
		}
		exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".a IS 'replacement'`, true)
		if !strings.Contains(dump(), "E'replacement'") {
			t.Fatal("replacement absent")
		}
		exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".a IS NULL`, false)
		exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".b IS ''`, true)
		if strings.Contains(dump(), "COMMENT ON COLUMN") {
			t.Fatal("removal serialized")
		}
		exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".b IS ' '`, true)
		if !strings.Contains(dump(), "IS E' ';") {
			t.Fatal("whitespace removed")
		}
	})
	t.Run("controls", func(t *testing.T) {
		if err := stuntgres.Reset(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, db, strings.NewReader(canonical)); err != nil {
			t.Fatal(err)
		}
		if dump() != canonical {
			t.Fatal("noncanonical roundtrip")
		}
		var v int
		if err := a.QueryRowContext(ctx, `SELECT a FROM "s.dot"."t.dot"`).Scan(&v); err != nil || v != 7 {
			t.Fatal(v, err)
		}
		for _, suffix := range []string{`COMMENT ON COLUMN "s.dot"."t.dot".missing IS 'late';`, `COMMENT ON ROLE postgres IS 'late';`} {
			if err := stuntgres.Restore(ctx, db, strings.NewReader(canonical+`COMMENT ON TABLE "s.dot"."t.dot" IS 'earlier';`+suffix)); err == nil {
				t.Fatal("accepted bad restore")
			}
			if dump() != canonical {
				t.Fatal("bad restore changed comments")
			}
		}
		exec(a, `ALTER SCHEMA "s.dot" RENAME TO renamed`, false)
		if !strings.Contains(dump(), `COMMENT ON SCHEMA "renamed" IS E'schema';`) {
			t.Fatal("rename lost comment")
		}
		if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE legacy(v INT); INSERT INTO legacy VALUES(7);")); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump(), "COMMENT") {
			t.Fatal("legacy stream gained comments")
		}
		t.Run("independent-owner-comments", func(t *testing.T) {
			for _, q := range []string{"CREATE USER mixed_table_owner", "CREATE USER mixed_schema_owner", "CREATE SCHEMA mixed AUTHORIZATION mixed_table_owner", "SET SESSION AUTHORIZATION mixed_table_owner", "CREATE TABLE mixed.t(v INT)", "RESET SESSION AUTHORIZATION", "ALTER SCHEMA mixed OWNER TO mixed_schema_owner"} {
				exec(a, q, false)
			}
			if _, err := a.ExecContext(ctx, "INSERT INTO mixed.t VALUES(7)"); err != nil {
				t.Fatal(err)
			}
			exec(a, "COMMENT ON TABLE mixed.t IS 'table text'", false)
			exec(a, "COMMENT ON COLUMN mixed.t.v IS 'column text'", true)
			exec(a, "COMMENT ON ROLE mixed_table_owner IS 'retained shared text'", true)
			mixed := dump()
			for _, entry := range []string{`CREATE SCHEMA "mixed" AUTHORIZATION "mixed_schema_owner";`, `SET SESSION AUTHORIZATION 'mixed_table_owner';`, `COMMENT ON TABLE "mixed"."t" IS E'table text';`, `COMMENT ON COLUMN "mixed"."t"."v" IS E'column text';`} {
				if !strings.Contains(mixed, entry) {
					t.Fatal("mixed-owner dump lacks independent payload", entry, mixed)
				}
			}
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, strings.NewReader(mixed)); err != nil {
				t.Fatal("mixed-owner commented stream cannot replay", err)
			}
			if dump() != mixed {
				t.Fatal("mixed-owner replay changed canonical stream")
			}
			var sentinel int
			if err := a.QueryRowContext(ctx, "SELECT v FROM mixed.t").Scan(&sentinel); err != nil || sentinel != 7 {
				t.Fatal("mixed-owner replay lost sentinel", sentinel, err)
			}
			exec(a, "SET SESSION AUTHORIZATION mixed_table_owner", false)
			for _, query := range []string{"COMMENT ON TABLE mixed.t IS 'denied'", "COMMENT ON COLUMN mixed.t.v IS 'denied'"} {
				_, err := a.ExecContext(ctx, query)
				diagnostic(err, "42501", "permission denied for schema mixed", "")
			}
			var forbidden int
			err := a.QueryRowContext(ctx, "SELECT v FROM mixed.t").Scan(&forbidden)
			if err := transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema mixed", Position: "15"}); err != nil {
				t.Fatal("public SELECT acquired private replay authority", err)
			}
			exec(a, "RESET SESSION AUTHORIZATION", false)
			if dump() != mixed {
				t.Fatal("post-Restore denials changed mixed-owner stream")
			}
		})
	})
	t.Run("authority", func(t *testing.T) {
		for _, q := range []string{"CREATE USER owner", "CREATE USER other", "CREATE SCHEMA owned AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE owned.t(v INT)", "COMMENT ON TABLE owned.t IS 'owner'"} {
			exec(a, q, false)
		}
		held, err := a.PrepareContext(ctx, "COMMENT ON TABLE owned.t IS 'prepared'")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		exec(a, "SET SESSION AUTHORIZATION other", false)
		// A separate public table keeps schema USAGE from masking ownership diagnostics.
		exec(b, "SET SESSION AUTHORIZATION owner", false)
		exec(b, "CREATE TABLE public.auth_table(v INT)", false)
		exec(b, "RESET SESSION AUTHORIZATION", false)
		for _, tc := range []struct{ q, code, message, detail string }{
			{"COMMENT ON TABLE public.auth_table IS 'bad'", "42501", "must be owner of table auth_table", ""},
			{"COMMENT ON COLUMN public.auth_table.v IS 'bad'", "42501", "must be owner of relation auth_table", ""},
			{"COMMENT ON ROLE other IS 'bad'", "42501", "permission denied", "The current user must have the CREATEROLE attribute."},
			{"COMMENT ON ROLE postgres IS 'bad'", "42501", "permission denied", "The current user must have the SUPERUSER attribute."},
			{"COMMENT ON ROLE absent IS NULL", "42704", `role "absent" does not exist`, ""},
		} {
			for _, prepared := range []bool{false, true} {
				_, err := transactionExecute(ctx, a, tc.q, prepared)
				diagnostic(err, tc.code, tc.message, tc.detail)
			}
		}
		if _, err := held.ExecContext(ctx); err == nil {
			t.Fatal("prepared authority cached")
		}
		exec(a, "RESET SESSION AUTHORIZATION", false)
		exec(a, "COMMENT ON ROLE owner IS 'shared'", false)
		exec(a, "COMMENT ON ROLE owner IS 'replacement'", true)
		if strings.Contains(dump(), "COMMENT ON ROLE") {
			t.Fatal("database dump imported role metadata")
		}
		exec(a, "DROP TABLE owned.t", false)
		exec(a, "CREATE TABLE owned.t(v INT)", false)
		exec(a, "SET SESSION AUTHORIZATION owner", false)
		_, err = held.ExecContext(ctx)
		diagnostic(err, "42501", "must be owner of table t", "")
		exec(a, "RESET SESSION AUTHORIZATION", false)
	})
	t.Run("locks-and-limits", func(t *testing.T) {
		exec(a, "CREATE TABLE locked(v INT)", false)
		exec(a, "COMMENT ON TABLE locked IS 'old'", false)
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "LOCK TABLE locked IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"COMMENT ON TABLE locked IS 'new'", "COMMENT ON COLUMN locked.v IS 'column'"} {
			stmt, err := b.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("utility Prepare waited", err)
			}
			bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			_, err = stmt.ExecContext(bounded)
			cancel()
			stmt.Close()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("AX did not block", err)
			}
		}
		if !strings.Contains(dump(), `COMMENT ON TABLE "public"."locked" IS E'old';`) {
			t.Fatal("cancel changed comment")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		exec(b, "COMMENT ON TABLE locked IS 'new'", true)
		tx, err = a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, "COMMENT ON TABLE locked IS NULL")
		diagnostic(err, "0A000", "statement is unsupported in an explicit transaction", "")
		tx.Rollback()
		tx, err = a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := tx.PrepareContext(ctx, "COMMENT ON TABLE locked IS NULL")
		if prepared != nil {
			prepared.Close()
		}
		diagnostic(err, "0A000", "statement is unsupported in an explicit transaction", "")
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump(), `COMMENT ON TABLE "public"."locked" IS E'new';`) {
			t.Fatal("explicit transaction rejection changed comment")
		}
		for _, q := range []string{"COMMENT ON TYPE integer IS 'x'", "COMMENT ON DATABASE d IS 'x'"} {
			_, err := a.ExecContext(ctx, q)
			var d *stuntgres.Error
			if !errors.As(err, &d) || d.Code != "0A000" {
				t.Fatal(q, err)
			}
		}
	})
}
