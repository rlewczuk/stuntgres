package smoke_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestSequenceDeleteBoundarySmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil { t.Fatal(err) }
	db := sql.OpenDB(connector)
	defer db.Close()
	conn := func() *sql.Conn {
		c, err := db.Conn(ctx)
		if err != nil { t.Fatal(err) }
		t.Cleanup(func() { c.Close() })
		return c
	}
	c, b := conn(), conn()
	insertSelectCount := func(t *testing.T, r sql.Result, err error, want int64) {
		t.Helper()
		if err != nil { t.Fatal(err) }
		if n, err := r.RowsAffected(); err != nil || n != want { t.Fatal(n,err,want) }
	}
	typeCoreDiagnostic := func(t *testing.T, err error, want stuntgres.Error) {
		t.Helper()
		if err := typeCoreDiagnostic(err,want); err != nil { t.Fatal(err) }
	}
	sequenceHeld := func(t *testing.T, c *sql.Conn, q string) *sql.Stmt {
		t.Helper()
		s, err := c.PrepareContext(ctx,q)
		if err != nil { t.Fatal("analysis-only Prepare",q,err) }
		return s
	}
	sequenceValue := func(t *testing.T, c *sql.Conn, q string, want int64) {
		t.Helper()
		var n int64
		if err := c.QueryRowContext(ctx,q).Scan(&n); err != nil || n != want { t.Fatal(q,n,err,want) }
	}
	exec := func(q string, want int64) {
		t.Helper()
		r, err := c.ExecContext(ctx, q)
		insertSelectCount(t, r, err, want)
	}
	rows := func(q string, columns, types []string, want [][]any) {
		t.Helper()
		r, err := c.QueryContext(ctx, q)
		if err := identifierCheckRows(r, err, columns, types, want); err != nil { t.Fatal(q,err) }
	}
	state := func(name string, n int64, called bool) {
		rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
	}
	unchanged := func() { rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}) }
	for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} { exec(q, 0) }
	exec("INSERT INTO t VALUES(1),(1),(2)", 3)
	for _, prepared := range []bool{false, true} {
		for _, target := range []string{"empty_t", "t"} {
			for _, tc := range []struct { q, arg string; want stuntgres.Error }{
				{"DELETE FROM " + target + " WHERE setval('s',$1::text::bigint)>0", "bad", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}},
				{"DELETE FROM " + target + " WHERE setval($1,nextval('nested'))>0", "missing", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}},
			} {
				invoke := func() (sql.Result, error) { return c.ExecContext(ctx, tc.q, tc.arg) }
				var s *sql.Stmt
				if prepared {
					s = sequenceHeld(t, c, tc.q)
					invoke = func() (sql.Result, error) { return s.ExecContext(ctx, tc.arg) }
				}
				_, err := invoke()
				if s != nil { s.Close() }
				typeCoreDiagnostic(t, err, tc.want)
				unchanged()
				rows("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, [][]any{})
				for _, seq := range []struct { name string; start int64 }{{"s",31},{"nested",47}} {
					state(seq.name,seq.start,false)
					var n int64
					typeCoreDiagnostic(t,c.QueryRowContext(ctx,"SELECT currval('"+seq.name+"')").Scan(&n),stuntgres.Error{Code:"55000",Message:`currval of sequence "`+seq.name+`" is not yet defined in this session`})
				}
				var n int64
				typeCoreDiagnostic(t,c.QueryRowContext(ctx,"SELECT lastval()").Scan(&n),stuntgres.Error{Code:"55000",Message:"lastval is not yet defined in this session"})
			}
		}
	}
	for _, prepared := range []bool{false,true} {
		for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0","DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
			exec("BEGIN",0)
			var r sql.Result
			var err error
			if prepared { s := sequenceHeld(t,c,q); r,err = s.ExecContext(ctx); s.Close() } else { r,err = c.ExecContext(ctx,q) }
			insertSelectCount(t,r,err,0)
			peer, stop := context.WithTimeout(ctx,2*time.Second)
			r,err = b.ExecContext(peer,"ALTER SEQUENCE s RESTART 80")
			stop()
			if err != nil { t.Fatal("undemanded DELETE holds sequence RX while initiating TX remains open",err) }
			insertSelectCount(t,r,err,0)
			exec("COMMIT",0)
			state("s",80,false)
			var n int64
			typeCoreDiagnostic(t,c.QueryRowContext(ctx,"SELECT currval('s')").Scan(&n),stuntgres.Error{Code:"55000",Message:`currval of sequence "s" is not yet defined in this session`})
		}
	}
	unchanged()
	// Native evaluates both logical children; decisive FALSE is not proof
	// of absence of sequence demand on this backend.
	exec("CREATE SEQUENCE decisive START 101 CACHE 1",0)
	for i,prepared := range []bool{false,true} {
		exec("BEGIN",0)
		q := "DELETE FROM t WHERE false AND nextval('decisive')>0"
		var r sql.Result
		var err error
		if prepared { held := sequenceHeld(t,c,q); r,err = held.ExecContext(ctx); held.Close() } else { r,err = c.ExecContext(ctx,q) }
		insertSelectCount(t,r,err,0)
		want := int64(103+i*3)
		state("decisive",want,true)
		sequenceValue(t,c,"SELECT currval('decisive')",want)
		sequenceValue(t,c,"SELECT lastval()",want)
		exec("ROLLBACK",0)
		unchanged()
		state("decisive",want,true)
	}
	for _, prepared := range []bool{false,true} {
		exec("CREATE SEQUENCE bounded MINVALUE 1 MAXVALUE 2 CACHE 1",0)
		q := "DELETE FROM t WHERE nextval('bounded')>0"
		var err error
		if prepared { s := sequenceHeld(t,c,q); _,err = s.ExecContext(ctx); s.Close() } else { _,err = c.ExecContext(ctx,q) }
		typeCoreDiagnostic(t,err,stuntgres.Error{Code:"2200H",Message:`nextval: reached maximum value of sequence "bounded" (2)`})
		unchanged()
		state("bounded",2,true)
		sequenceValue(t,c,"SELECT currval('bounded')",2)
		sequenceValue(t,c,"SELECT lastval()",2)
		exec("DROP SEQUENCE bounded",0)
	}
	for _, q := range []string{"CREATE SCHEMA a","CREATE SCHEMA b","CREATE SEQUENCE b.x START 10 CACHE 1","CREATE SEQUENCE b.y START 20 CACHE 1","CREATE TABLE b.held(id INT)","SET search_path TO a,b"} { exec(q,0) }
	exec("INSERT INTO b.held VALUES(7)",1)
	q := "DELETE FROM b.held WHERE setval('x',$1::bigint)>0 AND nextval('y')>0"
	s := sequenceHeld(t,c,q)
	defer s.Close()
	state("b.x",10,false)
	state("b.y",20,false)
	baseline, baselineErr := s.ExecContext(ctx,int64(10))
	insertSelectCount(t,baseline,baselineErr,1)
	rows("SELECT id FROM b.held",[]string{"id"},[]string{"INT4"},[][]any{})
	exec("INSERT INTO b.held VALUES(7)",1)
	exec("CREATE SEQUENCE a.x START 100 CACHE 1",0)
	exec("ALTER SEQUENCE b.y RENAME TO identity",0)
	failed := func() {
		_,err := s.ExecContext(ctx,int64(150))
		typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42P01",Message:`relation "y" does not exist`,Position:strconv.Itoa(strings.Index(q,"'y'")+1)})
		rows("SELECT id FROM b.held",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}})
	}
	failed()
	state("a.x",100,false)
	state("b.x",10,true)
	state("b.identity",20,true)
	sequenceValue(t,c,"SELECT currval('b.x')",10)
	sequenceValue(t,c,"SELECT currval('b.identity')",20)
	sequenceValue(t,c,"SELECT lastval()",20)
	var shadowMemory int64
	typeCoreDiagnostic(t,c.QueryRowContext(ctx,"SELECT currval('a.x')").Scan(&shadowMemory),stuntgres.Error{Code:"55000",Message:`currval of sequence "x" is not yet defined in this session`})
	exec("CREATE SEQUENCE a.y START 200 CACHE 1",0)
	deleted := func(n int64) {
		r,err := s.ExecContext(ctx,n)
		insertSelectCount(t,r,err,1)
		rows("SELECT id FROM b.held",[]string{"id"},[]string{"INT4"},[][]any{})
	}
	deleted(150)
	exec("INSERT INTO b.held VALUES(7)",1)
	exec("SET search_path TO b",0)
	failed()
	state("a.x",150,true)
	state("a.y",200,true)
	state("b.x",10,true)
	state("b.identity",20,true)
	sequenceValue(t,c,"SELECT currval('a.x')",150)
	sequenceValue(t,c,"SELECT currval('a.y')",200)
	sequenceValue(t,c,"SELECT lastval()",200)
	exec("SET search_path TO a,b",0)
	deleted(151)
	state("a.x",151,true)
	state("a.y",201,true)
	exec("SET search_path TO public",0)
	for _, prepared := range []bool{false,true} {
		q := "DELETE FROM s WHERE nextval('s')>0"
		var err error
		if prepared { held := sequenceHeld(t,c,q); _,err = held.ExecContext(ctx); held.Close() } else { _,err = c.ExecContext(ctx,q) }
		typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42809",Message:`cannot change sequence "s"`})
		state("s",80,false)
	}
	exec("DELETE FROM t WHERE id=2",1)
	exec("CREATE SEQUENCE producer START 10 CACHE 1",0)
	exec("CREATE TABLE dst(id INT,n BIGINT)",0)
	producer := sequenceHeld(t,c,"INSERT INTO dst SELECT id,nextval('producer') FROM t ORDER BY id")
	state("producer",10,false)
	r,err := producer.ExecContext(ctx)
	producer.Close()
	insertSelectCount(t,r,err,2)
	rows("SELECT id,n FROM dst ORDER BY n",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(10)},{int64(1),int64(11)}})
	sequenceValue(t,c,"SELECT nextval('producer')",12)
}
