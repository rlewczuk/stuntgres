package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestSequenceCreateRestartSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			ctx := context.Background()
			var notices []stuntgres.Error
			base, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
			defer db.Close()
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			execute := func(q string) error {
				var r sql.Result
				var e error
				if prepared {
					var s *sql.Stmt
					s, e = c.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal("Prepare", q, e)
					}
					defer s.Close()
					r, e = s.ExecContext(ctx)
				} else {
					r, e = c.ExecContext(ctx, q)
				}
				if e == nil {
					if n, e := r.RowsAffected(); e != nil || n != 0 {
						t.Fatal(q, n, e)
					}
				}
				return e
			}
			exec := func(q string) {
				if e := execute(q); e != nil {
					t.Fatal(q, e)
				}
			}
			value := func(q string, want int64) {
				var n int64
				if e := c.QueryRowContext(ctx, q).Scan(&n); e != nil || n != want {
					t.Fatal(q, n, e)
				}
			}
			diagnostic := func(e error, code, message, position string) {
				var got *stuntgres.Error
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
				if !errors.As(e, &got) || *got != want {
					t.Fatalf("got %#v (%v), want %#v", got, e, want)
				}
			}
			state := func(name string, last int64, called bool) {
				rows, e := c.QueryContext(ctx, "SELECT * FROM "+name)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				cols, e := rows.Columns()
				if e != nil || strings.Join(cols, ",") != "last_value,log_cnt,is_called" {
					t.Fatal(cols, e)
				}
				types, e := rows.ColumnTypes()
				if e != nil || len(types) != 3 {
					t.Fatal(types, e)
				}
				for i, want := range []string{"INT8", "INT8", "BOOL"} {
					if types[i].DatabaseTypeName() != want {
						t.Fatal(types[i])
					}
				}
				var n, log int64
				var b bool
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				if e = rows.Scan(&n, &log, &b); e != nil || n != last || log != 0 || b != called {
					t.Fatal(name, n, log, b, e)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra row", rows.Err())
				}
			}
			exec("CREATE SEQUENCE sentinel START 41")
			value("SELECT nextval('sentinel')", 41)
			for _, tc := range []struct {
				name, ddl string
				initial   int64
			}{
				{"bare", "CREATE SEQUENCE bare START 5 RESTART", 5},
				{"explicit", "CREATE SEQUENCE explicit START 5 RESTART 9", 9},
				{"with_value", "CREATE SEQUENCE with_value START 5 RESTART WITH 9", 9},
				{"temporary", "CREATE TEMP SEQUENCE temporary START 5 RESTART 9", 9},
				{"unlogged", "CREATE UNLOGGED SEQUENCE unlogged START 5 RESTART WITH 9", 9},
				{"absent", "CREATE SEQUENCE IF NOT EXISTS absent START 5 RESTART WITH 9", 9},
			} {
				exec(tc.ddl)
				state(tc.name, tc.initial, false)
				value("SELECT currval('sentinel')", 41)
				value("SELECT lastval()", 41)
				var n int64
				diagnostic(func() error { return c.QueryRowContext(ctx, "SELECT currval('"+tc.name+"')").Scan(&n) }(), "55000", `currval of sequence "`+tc.name+`" is not yet defined in this session`, "")
				value("SELECT nextval('"+tc.name+"')", tc.initial)
				exec("ALTER SEQUENCE " + tc.name + " RESTART")
				state(tc.name, 5, false)
				value("SELECT nextval('"+tc.name+"')", 5)
				value("SELECT nextval('sentinel')", 42)
				value("SELECT setval('sentinel',41,true)", 41)
			}
			for _, tc := range []struct{ name, options, code, message string }{
				{"low", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
				{"high", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 11", "22023", "RESTART value (11) cannot be greater than MAXVALUE (10)"},
				{"duplicate", "START 5 RESTART 9 RESTART WITH 8", "42601", "conflicting or redundant options"},
			} {
				q := "CREATE SEQUENCE " + tc.name + " " + tc.options
				pos := ""
				if tc.code == "42601" {
					pos = strconv.Itoa(strings.LastIndex(q, "RESTART") + 1)
				}
				diagnostic(execute(q), tc.code, tc.message, pos)
				var n int64
				q = "SELECT last_value FROM " + tc.name
				diagnostic(c.QueryRowContext(ctx, q).Scan(&n), "42P01", `relation "`+tc.name+`" does not exist`, "24")
				state("sentinel", 41, true)
				value("SELECT currval('sentinel')", 41)
				value("SELECT lastval()", 41)
			}
			exec("CREATE SEQUENCE IF NOT EXISTS sentinel RESTART 0")
			wantNotice := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sentinel" already exists, skipping`}
			if len(notices) != 1 || notices[0] != wantNotice {
				t.Fatalf("notices %#v want %#v", notices, wantNotice)
			}
			state("sentinel", 41, true)
			value("SELECT currval('sentinel')", 41)
			value("SELECT lastval()", 41)
			exec("CREATE SEQUENCE cached START 5 RESTART WITH 9 CACHE 3")
			state("cached", 9, false)
			value("SELECT nextval('cached')", 9)
			state("cached", 11, true)
			value("SELECT nextval('cached')", 10)
			state("cached", 11, true)
			if prepared {
				s, e := c.PrepareContext(ctx, "CREATE SEQUENCE retained START 5 RESTART WITH 9")
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				for range 2 {
					r, e := s.ExecContext(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if n, e := r.RowsAffected(); e != nil || n != 0 {
						t.Fatal(n, e)
					}
					state("retained", 9, false)
					value("SELECT nextval('retained')", 9)
					exec("ALTER SEQUENCE retained RESTART")
					value("SELECT nextval('retained')", 5)
					exec("DROP SEQUENCE retained")
				}
			}
			exec("CREATE SEQUENCE roundtrip START 5 RESTART WITH 9")
			state("roundtrip", 9, false)
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			state("roundtrip", 9, false)
			var redump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("roundtrip dump", err, redump.String())
			}
			value("SELECT nextval('roundtrip')", 9)
			exec("ALTER SEQUENCE roundtrip RESTART")
			state("roundtrip", 5, false)
			value("SELECT nextval('roundtrip')", 5)
		})
	}
}
