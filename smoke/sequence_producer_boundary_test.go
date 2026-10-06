package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

// Standalone and suitable for TestSequenceSmoke's producer-boundary subtest.
func TestSequenceProducerBoundarySmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	conn := func() *sql.Conn {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c, b := conn(), conn()
	exec := func(q string, want int64) {
		r, err := c.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	rows := func(q string, names, types []string, want [][]any) {
		r, err := c.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		defer r.Close()
		cols, err := r.Columns()
		if err != nil || !reflect.DeepEqual(cols, names) {
			t.Fatal(cols, err, names)
		}
		meta, err := r.ColumnTypes()
		if err != nil || len(meta) != len(types) {
			t.Fatal(meta, err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta[i], typ)
			}
		}
		got := make([][]any, 0)
		for r.Next() {
			v, dst := make([]any, len(names)), make([]any, len(names))
			for i := range v {
				dst[i] = &v[i]
			}
			if err := r.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if r.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want, r.Err())
		}
	}
	state := func(name string, v int64, called bool) {
		rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{v, called}})
	}
	for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
		exec(q, 0)
	}
	exec("INSERT INTO src VALUES(1)", 1)
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
			for _, tc := range []struct {
				q    string
				arg  any
				want stuntgres.Error
			}{
				{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}},
				{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}},
			} {
				var err error
				if prepared {
					s, e := c.PrepareContext(ctx, tc.q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(ctx, tc.arg)
					s.Close()
				} else {
					_, err = c.ExecContext(ctx, tc.q, tc.arg)
				}
				diagnostic(err, tc.want)
				rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, [][]any{})
				state("s", 31, false)
				state("nested", 47, false)
				for _, name := range []string{"s", "nested"} {
					var remembered int64
					diagnostic(c.QueryRowContext(ctx, "SELECT currval('"+name+"')").Scan(&remembered), stuntgres.Error{Code: "55000", Message: `currval of sequence "` + name + `" is not yet defined in this session`})
				}
				var remembered int64
				diagnostic(c.QueryRowContext(ctx, "SELECT lastval()").Scan(&remembered), stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"})
			}
		}
	}
	rows("SELECT nextval('nested')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(47)}})
	for _, prepared := range []bool{false, true} {
		exec("BEGIN", 0)
		q := "INSERT INTO dst SELECT nextval('s') FROM src WHERE id=99"
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			r, e := s.ExecContext(ctx)
			s.Close()
			if e != nil {
				t.Fatal(e)
			}
			if n, e := r.RowsAffected(); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		} else {
			exec(q, 0)
		}
		// The source-table AS lock remains with A; the peer touches only s.
		peer, stop := context.WithTimeout(ctx, 2*time.Second)
		_, err := b.ExecContext(peer, "ALTER SEQUENCE s RESTART WITH 80")
		stop()
		if err != nil {
			t.Fatal("undemanded INSERT blocked peer RESTART", err)
		}
		exec("COMMIT", 0)
		rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, [][]any{})
		state("s", 80, false)
		rows("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(80)}})
	}
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10 CACHE 1", "CREATE SEQUENCE b.t START 20 CACHE 1", "CREATE TABLE b.dst(id BIGINT,n BIGINT,m BIGINT,day DATE,payload BYTEA,label TEXT)", "SET search_path TO a,b"} {
		exec(q, 0)
	}
	q := "INSERT INTO b.dst SELECT $1::text::bigint,nextval('s'),nextval('t'),DATE '2024-02-29',$3,$2::text"
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exec("CREATE SEQUENCE a.s START 100 CACHE 1", 0)
	exec("ALTER TABLE b.t RENAME TO identity", 0)
	missing := func() {
		_, err := s.ExecContext(ctx, "7", "first", []byte{0, 1})
		diagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: strconv.Itoa(strings.Index(q, "'t'") + 1)})
	}
	missing()
	rows("SELECT id FROM b.dst", []string{"id"}, []string{"INT8"}, [][]any{})
	state("a.s", 100, false)
	state("b.s", 10, false)
	state("b.identity", 20, false)
	exec("CREATE SEQUENCE a.t START 200 CACHE 1", 0)
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
	exec("SET search_path TO b", 0)
	missing()
	state("a.s", 100, true)
	state("a.t", 200, true)
	state("b.s", 10, false)
	exec("SET search_path TO a,b", 0)
	insert("8", "second")
	rows("SELECT * FROM b.dst ORDER BY id", []string{"id", "n", "m", "day", "payload", "label"}, []string{"INT8", "INT8", "INT8", "DATE", "BYTEA", "TEXT"}, [][]any{{int64(7), int64(100), int64(200), "2024-02-29", []byte{0, 1}, "first"}, {int64(8), int64(101), int64(201), "2024-02-29", []byte{0, 1}, "second"}})
	state("a.s", 101, true)
	state("a.t", 201, true)
	state("b.s", 10, false)
	state("b.identity", 20, false)
}
