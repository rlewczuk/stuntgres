package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestSequenceRegclassInputSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil { t.Fatal(err) }
	db := sql.OpenDB(connector)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	exec := func(q string) {
		r, err := c.ExecContext(ctx, q)
		if err != nil { t.Fatal(q, err) }
		if n, err := r.RowsAffected(); err != nil || n != 0 { t.Fatal(q, n, err) }
	}
	diagnostic := func(err error, want stuntgres.Error) {
		if e := typeCoreDiagnostic(err, want); e != nil { t.Fatal(e) }
	}
	for _, q := range []string{"CREATE SEQUENCE s START 17 CACHE 1", "CREATE SEQUENCE t START 31 CACHE 1", "CREATE TABLE empty_table(v INT)", "CREATE TABLE nullable_table(v BIGINT)"} { exec(q) }
	result, err := c.ExecContext(ctx, "INSERT INTO nullable_table VALUES(NULL)")
	if err != nil { t.Fatal(err) }
	if n, err := result.RowsAffected(); err != nil || n != 1 { t.Fatal(n, err) }
	held, err := c.PrepareContext(ctx, "SELECT nextval($1)")
	if err != nil { t.Fatal(err) }
	defer held.Close()
	for _, input := range []string{"s--suffix", "s/*suffix*/"} {
		for _, q := range []string{"SELECT nextval($1) FROM empty_table", "SELECT setval($1,$2)", "SELECT setval($1,v) FROM nullable_table", "SELECT setval($1,nextval('t'))"} {
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil { t.Fatal(err) }
			args := []any{input}
			if q == "SELECT setval($1,$2)" { args = append(args, nil) }
			var n any
			err = stmt.QueryRowContext(ctx, args...).Scan(&n)
			stmt.Close()
			diagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "`+input+`" does not exist`, Context: "unnamed portal parameter $1 = '...'"})
		}
		var n int64
		diagnostic(held.QueryRowContext(ctx, input).Scan(&n), stuntgres.Error{Code: "42P01", Message: `relation "`+input+`" does not exist`, Context: "unnamed portal parameter $1 = '...'"})
		for _, name := range []string{"s", "t"} {
			diagnostic(c.QueryRowContext(ctx, "SELECT currval('"+name+"')").Scan(&n), stuntgres.Error{Code: "55000", Message: `currval of sequence "`+name+`" is not yet defined in this session`})
		}
		diagnostic(c.QueryRowContext(ctx, "SELECT lastval()").Scan(&n), stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"})
	}
	for _, q := range []string{`CREATE SEQUENCE "s--suffix" START 41 CACHE 1`, `CREATE SEQUENCE "s/*suffix*/" START 51 CACHE 1`, `CREATE SEQUENCE "s-other" START 61 CACHE 1`, `CREATE SEQUENCE "0" START 71 CACHE 1`, `CREATE SEQUENCE "-" START 81 CACHE 1`, `CREATE SEQUENCE "4294967295" START 91 CACHE 1`} { exec(q) }
	for _, tc := range []struct{ input string; want int64 }{{"s--suffix", 41}, {"s/*suffix*/", 51}, {"s-other", 61}, {" 0 ", 71}, {" - ", 81}, {" 4294967295 ", 91}, {"s", 17}, {"t", 31}} {
		var n int64
		if err := held.QueryRowContext(ctx, tc.input).Scan(&n); err != nil || n != tc.want { t.Fatal(tc.input, n, tc.want, err) }
	}
	projection, err := c.PrepareContext(ctx, "SELECT $1::regclass")
	if err != nil { t.Fatal(err) }
	defer projection.Close()
	for _, tc := range []struct{ input, want string }{{"0", "-"}, {"-", "-"}, {"4294967295", "4294967295"}, {" 0 ", `"0"`}, {" - ", `"-"`}, {" 4294967295 ", `"4294967295"`}} {
		var name string
		if err := projection.QueryRowContext(ctx, tc.input).Scan(&name); err != nil || name != tc.want { t.Fatal(tc.input, name, tc.want, err) }
	}
}
