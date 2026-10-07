package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestSequenceArithmeticCompositionSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	sequenceArithmeticComposition(t, ctx, c, peer, "arith_comp", func(err error, want stuntgres.Error) { sequenceArithmeticDiagnostic(t, err, want) })
}

func sequenceArithmeticDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic=%#v (%v), want=%#v", got, err, want)
	}
}

func sequenceArithmeticComposition(t *testing.T, ctx context.Context, c, peer *sql.Conn, schema string, diagnostic func(error, stuntgres.Error)) {
	t.Helper()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := c.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != want {
			t.Fatal(q, n, err, "want", want)
		}
	}
	exec("CREATE SCHEMA "+schema, 0)
	exec("SET search_path TO "+schema, 0)
	defer func() {
		_, _ = c.ExecContext(ctx, "ROLLBACK")
		_, _ = c.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	}()
	check := func(rows *sql.Rows, err error, labels, types []string, want [][]any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, labels) {
			t.Fatal(names, labels, err)
		}
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(types) {
			t.Fatal(meta, err)
		}
		for i, typ := range types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(i, meta[i].DatabaseTypeName(), typ)
			}
		}
		var got [][]any
		for rows.Next() {
			values := make([]sql.NullInt64, len(types))
			args := make([]any, len(types))
			for i := range values {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			row := make([]any, len(types))
			for i, v := range values {
				if v.Valid {
					row[i] = v.Int64
				}
			}
			got = append(got, row)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("rows=%#v want=%#v err=%v", got, want, err)
		}
	}
	query := func(q string, labels, types []string, want [][]any, args ...any) {
		t.Helper()
		r, err := c.QueryContext(ctx, q, args...)
		check(r, err, labels, types, want)
	}
	value := func(q string, n int64) { t.Helper(); query(q, []string{"?column?"}, []string{"INT8"}, [][]any{{n}}) }
	failure := func(q string, want stuntgres.Error, args ...any) {
		t.Helper()
		r, err := c.QueryContext(ctx, q, args...)
		if r != nil {
			defer r.Close()
			for r.Next() {
			}
			if err == nil {
				err = r.Err()
			}
		}
		diagnostic(err, want)
		s, prepareErr := c.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatal("import happened at Prepare", prepareErr)
		}
		defer s.Close()
		r, err = s.QueryContext(ctx, args...)
		if r != nil {
			defer r.Close()
			for r.Next() {
			}
			if err == nil {
				err = r.Err()
			}
		}
		diagnostic(err, want)
	}
	exec("CREATE TABLE src(id INT, nullable BIGINT)", 0)
	exec("INSERT INTO src VALUES(1,NULL),(2,7)", 2)
	exec("CREATE TABLE empty_src(id INT)", 0)
	// Separate direction and dispatch cases catch right-child volatility lost under a cast.
	for _, side := range []string{"left", "right"} {
		for _, held := range []bool{false, true} {
			name := fmt.Sprintf("demand_%s_%t", side, held)
			exec("CREATE SEQUENCE "+name, 0)
			expression := "nextval('" + name + "')+0"
			if side == "right" {
				expression = "0+nextval('" + name + "')"
			}
			for _, source := range []string{"src WHERE id=99", "empty_src"} {
				q := "SELECT CAST(" + expression + " AS integer) FROM " + source
				if held {
					s, err := c.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(q, err)
					}
					r, err := s.QueryContext(ctx)
					check(r, err, []string{"int4"}, []string{"INT4"}, nil)
					s.Close()
				} else {
					query(q, []string{"int4"}, []string{"INT4"}, nil)
				}
			}
			value("SELECT nextval('"+name+"')+0 FROM src WHERE id=1", 1)
			q := "SELECT CAST(" + expression + " AS integer) FROM src ORDER BY id"
			if held {
				s, err := c.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				r, err := s.QueryContext(ctx)
				check(r, err, []string{"int4"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
				s.Close()
			} else {
				query(q, []string{"int4"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
			}
			value("SELECT 0+currval('"+name+"') FROM src WHERE id=1", 3)
		}
	}
	// Stable right children must also remain row-owned beneath a non-identity cast.
	exec("CREATE SEQUENCE stable_s", 0)
	for _, source := range []string{"empty_src", "src WHERE id=99"} {
		query("SELECT CAST(0+currval('stable_s') AS integer) FROM "+source, []string{"int4"}, []string{"INT4"}, nil)
	}
	failure("SELECT CAST(0+currval('stable_s') AS integer) FROM src WHERE id=1",
		stuntgres.Error{Code: "55000", Message: `currval of sequence "stable_s" is not yet defined in this session`})
	value("SELECT nextval('stable_s')+0 FROM src WHERE id=1", 1)
	query("SELECT CAST(0+currval('stable_s') AS integer),CAST(0+lastval() AS integer) FROM src ORDER BY id",
		[]string{"int4", "int4"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}})
	// Ordinary nested arguments and setval's own effect each occur exactly once.
	exec("CREATE SEQUENCE nested START 10", 0)
	exec("CREATE SEQUENCE assigned", 0)
	value("SELECT 1+setval('assigned',nextval('nested')+5,false) FROM src WHERE id=1", 16)
	value("SELECT nextval('assigned')+nextval('nested') FROM src WHERE id=1", 26)
	value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
	// Imported conversions beneath nested arithmetic beat volatile effects even without rows.
	for _, source := range []string{"empty_src", "src WHERE id=99", "src WHERE id=1"} {
		failure("SELECT nextval('nested')+setval('assigned',($1::text::bigint)+1) FROM "+source,
			stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}, "bad")
		failure("SELECT nextval('nested')+setval($1,nextval('assigned')+1) FROM "+source,
			stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}, "missing")
	}
	value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
	value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
	exec("CREATE TABLE import_dst(v BIGINT)", 0)
	for _, source := range []string{"empty_src", "src WHERE id=99", "src WHERE id=1"} {
		for _, imported := range []struct {
			expression string
			arg        any
			want       stuntgres.Error
		}{
			{"nextval('nested')+setval('assigned',($1::text::bigint)+1)", "bad",
				stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}},
			{"nextval('nested')+setval($1,nextval('assigned')+1)", "missing",
				stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}},
		} {
			queries := []string{"INSERT INTO import_dst SELECT " + imported.expression + " FROM " + source}
			if source == "empty_src" {
				queries = append(queries, "DELETE FROM empty_src WHERE "+imported.expression+">0",
					"DELETE FROM src WHERE "+imported.expression+">0")
			}
			for _, q := range queries {
				_, err := c.ExecContext(ctx, q, imported.arg)
				diagnostic(err, imported.want)
				stmt, err := c.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal("consumer import at Prepare", err)
				}
				_, err = stmt.ExecContext(ctx, imported.arg)
				stmt.Close()
				diagnostic(err, imported.want)
			}
		}
	}
	query("SELECT v FROM import_dst", []string{"v"}, []string{"INT8"}, nil)
	query("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
	value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
	// Known NULL discards nested effects; row-derived NULL still evaluates all arguments.
	query("SELECT setval('assigned',nextval('nested')+1,NULL)+0 FROM src WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{nil}})
	value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
	query("SELECT setval('assigned',nextval('nested')+nullable,false)+0 FROM src WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{nil}})
	value("SELECT currval('nested')+0 FROM src WHERE id=1", 12)
	value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
	// Whole retained literal group fails before the earlier arithmetic sibling can allocate.
	exec("CREATE SEQUENCE first_s START 30", 0)
	exec("CREATE SEQUENCE second_s START 70", 0)
	q := "SELECT nextval('first_s')+nextval('second_s') FROM src WHERE id=1"
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exec("ALTER SEQUENCE second_s RENAME TO renamed_s", 0)
	r, err := s.QueryContext(ctx)
	if r != nil {
		r.Close()
	}
	diagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "second_s" does not exist`, Position: "35"})
	value("SELECT nextval('first_s')+0 FROM src WHERE id=1", 30)
	value("SELECT nextval('renamed_s')+0 FROM src WHERE id=1", 70)
	// Failure is row-owned, after one demanded value; neither consumer publishes partial rows.
	exec("CREATE TABLE divisor(v BIGINT)", 0)
	exec("INSERT INTO divisor VALUES(0)", 1)
	exec("CREATE TABLE dst(v BIGINT)", 0)
	exec("CREATE SEQUENCE late_s", 0)
	_, err = c.ExecContext(ctx, "INSERT INTO dst SELECT nextval('late_s')/v FROM divisor")
	diagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
	value("SELECT currval('late_s')+0 FROM src WHERE id=1", 1)
	exec("CREATE TABLE doomed(v BIGINT)", 0)
	exec("INSERT INTO doomed VALUES(9223372036854775807)", 1)
	_, err = c.ExecContext(ctx, "DELETE FROM doomed WHERE v+nextval('late_s')>0")
	diagnostic(err, stuntgres.Error{Code: "22003", Message: "bigint out of range"})
	query("SELECT v FROM doomed", []string{"v"}, []string{"INT8"}, [][]any{{int64(9223372036854775807)}})
	value("SELECT currval('late_s')+0 FROM src WHERE id=1", 2)
	// Frozen public metadata and REGCLASS presentation coexist with demanded arithmetic output.
	r, err = c.QueryContext(ctx, "SELECT 'late_s'::regclass,nextval('late_s')+0 FROM src WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	meta, err := r.ColumnTypes()
	if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "REGCLASS" || meta[1].DatabaseTypeName() != "INT8" {
		t.Fatal(meta, err)
	}
	names, err := r.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"regclass", "?column?"}) {
		t.Fatal(names, err)
	}
	if _, err := peer.ExecContext(ctx, "ALTER SEQUENCE "+schema+".late_s RENAME TO frozen_s"); err != nil {
		t.Fatal(err)
	}
	if !r.Next() {
		t.Fatal(r.Err())
	}
	var label string
	var n int64
	if err := r.Scan(&label, &n); err != nil || label != "late_s" || n != 3 {
		t.Fatal(label, n, err)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal(r.Err())
	}
	r.Close()
	// Restart is transactional; ordinary nextval/setval arithmetic remains nonrollback.
	exec("BEGIN", 0)
	exec("ALTER SEQUENCE frozen_s RESTART 100", 0)
	exec("INSERT INTO dst SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 1)
	exec("ROLLBACK", 0)
	query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
	value("SELECT 0+currval('frozen_s') FROM src WHERE id=1", 100)
	value("SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 4)
	exec("BEGIN", 0)
	value("SELECT setval('frozen_s',40+2,true)+0 FROM src WHERE id=1", 42)
	exec("INSERT INTO dst SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 1)
	exec("ROLLBACK", 0)
	query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
	value("SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 44)
	exec("BEGIN", 0)
	exec("ALTER SEQUENCE frozen_s RESTART 200", 0)
	exec("INSERT INTO dst SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 1)
	exec("COMMIT", 0)
	query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, [][]any{{int64(200)}})
	value("SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 201)
	// A changed search path invalidates the entire held arithmetic literal group.
	a, b := schema+"_path_a", schema+"_path_b"
	exec("CREATE SCHEMA "+a, 0)
	exec("CREATE SCHEMA "+b, 0)
	defer func() {
		_, _ = c.ExecContext(ctx, "DROP SCHEMA "+a+" CASCADE")
		_, _ = c.ExecContext(ctx, "DROP SCHEMA "+b+" CASCADE")
	}()
	exec("CREATE SEQUENCE "+a+".path_first START 10", 0)
	exec("CREATE SEQUENCE "+a+".path_second START 20", 0)
	exec("CREATE SEQUENCE "+b+".path_first START 100", 0)
	exec("SET search_path TO "+a+","+schema, 0)
	pathQuery := "SELECT nextval('path_first')+nextval('path_second') FROM src WHERE id=1"
	pathStmt, err := c.PrepareContext(ctx, pathQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer pathStmt.Close()
	exec("SET search_path TO "+b+","+schema, 0)
	r, err = pathStmt.QueryContext(ctx)
	if r != nil {
		r.Close()
	}
	diagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "path_second" does not exist`, Position: "38"})
	value("SELECT nextval('"+b+".path_first')+0 FROM src WHERE id=1", 100)
	exec("SET search_path TO "+a+","+schema, 0)
	r, err = pathStmt.QueryContext(ctx)
	check(r, err, []string{"?column?"}, []string{"INT8"}, [][]any{{int64(30)}})
	value("SELECT currval('path_first')+0 FROM src WHERE id=1", 10)
	value("SELECT currval('path_second')+0 FROM src WHERE id=1", 20)
}
