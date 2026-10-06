package stuntgres_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestSequenceProducerBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Run("InputBeforeDemand", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
			sequenceExec(t, c, q)
		}
		result, err := c.ExecContext(ctx, "INSERT INTO src VALUES(1)")
		insertSelectCount(t, result, err, 1)
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
				for _, tc := range []struct {
					query string
					arg   any
					want  stuntgres.Error
				}{
					{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}},
					{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}},
				} {
					exec := func() (sql.Result, error) { return c.ExecContext(ctx, tc.query, tc.arg) }
					if prepared {
						s, err := c.PrepareContext(ctx, tc.query)
						if err != nil {
							t.Fatal("Prepare imported input", err)
						}
						exec = func() (sql.Result, error) { return s.ExecContext(ctx, tc.arg) }
						defer s.Close()
					}
					_, err := exec()
					typeCoreDiagnostic(t, err, tc.want)
					rows, err := c.QueryContext(ctx, "SELECT v FROM dst")
					queryCoreRows(t, rows, err, []string{"v"}, []string{"INT8"}, [][]any{}, false)
					for _, state := range []struct {
						name  string
						start int64
					}{{"s", 31}, {"nested", 47}} {
						rows, err := c.QueryContext(ctx, "SELECT last_value,is_called FROM "+state.name)
						queryCoreRows(t, rows, err, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{state.start, false}}, false)
						var remembered int64
						typeCoreDiagnostic(t, c.QueryRowContext(ctx, "SELECT currval('"+state.name+"')").Scan(&remembered), stuntgres.Error{Code: "55000", Message: `currval of sequence "` + state.name + `" is not yet defined in this session`})
					}
					var remembered int64
					typeCoreDiagnostic(t, c.QueryRowContext(ctx, "SELECT lastval()").Scan(&remembered), stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"})
				}
			}
		}
		sequenceValue(t, c, "SELECT nextval('s')", 31)
		sequenceValue(t, c, "SELECT nextval('nested')", 47)
	})
	t.Run("UndemandedTransaction", func(t *testing.T) {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		a, b := sequenceConn(t, db), sequenceConn(t, db)
		for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
			sequenceExec(t, a, q)
		}
		result, err := a.ExecContext(ctx, "INSERT INTO src VALUES(1)")
		insertSelectCount(t, result, err, 1)
		for _, prepared := range []bool{false, true} {
			sequenceExec(t, a, "BEGIN")
			q := "INSERT INTO dst SELECT nextval('s') FROM src WHERE id=99"
			var r sql.Result
			var err error
			if prepared {
				s := sequenceHeld(t, a, q)
				r, err = s.ExecContext(ctx)
				s.Close()
			} else {
				r, err = a.ExecContext(ctx, q)
			}
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			// A remains open and holds source-table AS, not sequence RX. B touches only s.
			peer, stop := context.WithTimeout(ctx, 2*time.Second)
			_, err = b.ExecContext(peer, "ALTER SEQUENCE s RESTART WITH 80")
			stop()
			if err != nil {
				t.Fatal("undemanded INSERT blocked peer RESTART", err)
			}
			sequenceExec(t, a, "COMMIT")
			rows, err := a.QueryContext(ctx, "SELECT v FROM dst")
			queryCoreRows(t, rows, err, []string{"v"}, []string{"INT8"}, [][]any{}, false)
			sequenceValue(t, a, "SELECT nextval('s')", 80)
		}
	})
	t.Run("RetainedGroup", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10 CACHE 1", "CREATE SEQUENCE b.t START 20 CACHE 1", "CREATE TABLE b.dst(id BIGINT,n BIGINT,m BIGINT,day DATE,payload BYTEA,label TEXT)", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		q := "INSERT INTO b.dst SELECT $1::text::bigint,nextval('s'),nextval('t'),DATE '2024-02-29',$3,$2::text"
		s := sequenceHeld(t, c, q)
		sequenceExec(t, c, "CREATE SEQUENCE a.s START 100 CACHE 1")
		sequenceExec(t, c, "ALTER TABLE b.t RENAME TO identity")
		missing := func() {
			_, err := s.ExecContext(ctx, "7", "first", []byte{0, 1})
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: strconv.Itoa(strings.Index(q, "'t'") + 1)})
		}
		missing()
		rows, err := c.QueryContext(ctx, "SELECT id FROM b.dst")
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT8"}, [][]any{}, false)
		sequenceExec(t, c, "CREATE SEQUENCE a.t START 200 CACHE 1")
		insert := func(id, label string) {
			r, err := s.ExecContext(ctx, id, label, []byte{0, 1})
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
		insert("7", "first")
		sequenceExec(t, c, "SET search_path TO b")
		missing()
		sequenceExec(t, c, "SET search_path TO a,b")
		insert("8", "second")
		rows, err = c.QueryContext(ctx, "SELECT * FROM b.dst ORDER BY id")
		queryCoreRows(t, rows, err, []string{"id", "n", "m", "day", "payload", "label"}, []string{"INT8", "INT8", "INT8", "DATE", "BYTEA", "TEXT"}, [][]any{{int64(7), int64(100), int64(200), "2024-02-29", []byte{0, 1}, "first"}, {int64(8), int64(101), int64(201), "2024-02-29", []byte{0, 1}, "second"}}, false)
		for _, state := range []struct {
			name   string
			value  int64
			called bool
		}{{"b.s", 10, false}, {"b.identity", 20, false}, {"a.s", 101, true}, {"a.t", 201, true}} {
			rows, err := c.QueryContext(ctx, "SELECT last_value,is_called FROM "+state.name)
			queryCoreRows(t, rows, err, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{state.value, state.called}}, false)
		}
	})
}
