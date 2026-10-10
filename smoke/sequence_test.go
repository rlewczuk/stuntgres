package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

// Exercises real public SQL and control APIs without catalog introspection.
func TestSequenceSmoke(t *testing.T) {
	t.Run("physical-end", TestSequenceTemporaryPhysicalEndSmoke)
	t.Run("prepared-invalidation", sequencePreparedSmoke)
	t.Run("merged-producer", TestSequenceMergedProducerSmoke)
	t.Run("producer-boundary", TestSequenceProducerBoundarySmoke)
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("sequence-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
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
	exec := func(c *sql.Conn, q string) {
		t.Helper()
		r, err := c.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(q, n, err)
		}
	}
	value := func(c *sql.Conn, q string, want int64) {
		t.Helper()
		var got int64
		if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s got %d want %d err %v", q, got, want, err)
		}
	}
	diagnostic := func(q string, want stuntgres.Error) {
		t.Helper()
		_, err := a.ExecContext(ctx, q)
		var got *stuntgres.Error
		if !errors.As(err, &got) || got.Code != want.Code || got.Message != want.Message || got.Position != want.Position || got.Detail != want.Detail || got.Hint != want.Hint {
			t.Fatalf("%s: got %#v want %#v", q, got, want)
		}
	}
	exec(a, "CREATE SEQUENCE defaults")
	for _, owner := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
		exec(a, "ALTER SEQUENCE defaults OWNER TO "+owner)
	}
	var parameterName string
	if err = a.QueryRowContext(ctx, "SELECT $1::regclass", "defaults").Scan(&parameterName); err != nil || parameterName != "defaults" {
		t.Fatal("direct regclass parameter", parameterName, err)
	}
	parameterProjection, err := a.PrepareContext(ctx, "SELECT $1::regclass")
	if err != nil {
		t.Fatal(err)
	}
	if err = parameterProjection.QueryRowContext(ctx, "defaults").Scan(&parameterName); err != nil || parameterName != "defaults" {
		t.Fatal("prepared regclass parameter", parameterName, err)
	}
	parameterProjection.Close()
	_, err = a.ExecContext(ctx, "SELECT setval($1,$2)", "missing", nil)
	var parameterError *stuntgres.Error
	parameterWant := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}
	if !errors.As(err, &parameterError) || *parameterError != parameterWant {
		t.Fatalf("regclass input before runtime NULL: %#v", parameterError)
	}
	diagnostic("SELECT currval('defaults')", stuntgres.Error{Code: "55000", Message: `currval of sequence "defaults" is not yet defined in this session`})
	diagnostic("SELECT lastval()", stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"})
	defaults, err := a.PrepareContext(ctx, "SELECT nextval('defaults')")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 2} {
		rows, err := defaults.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].Name() != "nextval" || types[0].DatabaseTypeName() != "INT8" {
			t.Fatal(types, err)
		}
		var v int64
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if err = rows.Scan(&v); err != nil || v != want {
			t.Fatal(v, err)
		}
		if rows.Next() {
			t.Fatal("extra row")
		}
		rows.Close()
	}
	defaults.Close()
	value(b, "SELECT nextval('defaults')", 3)
	value(a, "SELECT currval('defaults')", 2)
	value(a, "SELECT lastval()", 2)
	value(a, "SELECT setval('defaults',42,true)", 42)
	value(a, "SELECT lastval()", 42)
	value(a, "SELECT nextval('defaults')", 43)
	value(a, "SELECT setval('defaults',7,false)", 7)
	value(a, "SELECT currval('defaults')", 43)
	value(a, "SELECT nextval('defaults')", 7)
	exec(a, "BEGIN")
	value(a, "SELECT setval('defaults',50)", 50)
	exec(a, "ROLLBACK")
	value(b, "SELECT nextval('defaults')", 51)
	r, err := a.ExecContext(ctx, "SELECT nextval('defaults')")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	exec(a, "CREATE SEQUENCE bag CACHE 1")
	const each = 20
	values := make(chan int64, each*2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range []*sql.Conn{a, b} {
		wg.Add(1)
		go func(c *sql.Conn) {
			defer wg.Done()
			for range each {
				var v int64
				if err := c.QueryRowContext(ctx, "SELECT nextval('bag')").Scan(&v); err != nil {
					failures <- err
					return
				}
				values <- v
			}
		}(c)
	}
	wg.Wait()
	close(values)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	seen := make(map[int64]int)
	for v := range values {
		seen[v]++
	}
	if len(seen) != each*2 {
		t.Fatal(seen)
	}
	for v := int64(1); v <= each*2; v++ {
		if seen[v] != 1 {
			t.Fatal("bag multiplicity", v, seen[v])
		}
	}
	exec(a, "CREATE SEQUENCE high CACHE 10")
	value(a, "SELECT nextval('high')", 1)
	value(b, "SELECT nextval('high')", 11)
	value(a, "SELECT nextval('high')", 2)
	value(a, "SELECT last_value FROM high", 20)
	exec(a, "CREATE SEQUENCE bounded MINVALUE 2 MAXVALUE 5 INCREMENT 2 START 2")
	value(a, "SELECT nextval('bounded')", 2)
	value(a, "SELECT nextval('bounded')", 4)
	diagnostic("SELECT nextval('bounded')", stuntgres.Error{Code: "2200H", Message: `nextval: reached maximum value of sequence "bounded" (5)`})
	exec(a, "ALTER SEQUENCE bounded CYCLE")
	value(a, "SELECT nextval('bounded')", 2)
	exec(a, "CREATE SEQUENCE restart")
	value(a, "SELECT nextval('restart')", 1)
	for _, commit := range []bool{false, true} {
		exec(a, "BEGIN")
		exec(a, "ALTER SEQUENCE restart RESTART 100")
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		done := make(chan error, 1)
		result := make(chan int64, 1)
		go func() {
			var v int64
			err := b.QueryRowContext(requestCtx, "SELECT nextval('restart')").Scan(&v)
			if err == nil {
				result <- v
			}
			done <- err
		}()
		if commit {
			exec(a, "COMMIT")
		} else {
			exec(a, "ROLLBACK")
		}
		if err := <-done; err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		want := int64(2)
		if commit {
			want = 100
		}
		if v := <-result; v != want {
			t.Fatal(v, want)
		}
	}
	exec(a, "CREATE SCHEMA moved")
	exec(a, "CREATE UNLOGGED SEQUENCE admin")
	exec(a, "ALTER SEQUENCE admin SET LOGGED")
	exec(a, "ALTER SEQUENCE admin OWNER TO postgres")
	exec(a, "ALTER SEQUENCE admin SET SCHEMA moved")
	exec(a, "ALTER SEQUENCE moved.admin RENAME TO renamed_admin")
	value(a, "SELECT nextval('moved.renamed_admin')", 1)
	diagnostic("ALTER TABLE moved.renamed_admin RENAME COLUMN last_value TO renamed", stuntgres.Error{Code: "42809", Message: `cannot rename columns of relation "renamed_admin"`, Detail: "This operation is not supported for sequences."})
	diagnostic("CREATE SEQUENCE invalid CACHE 0", stuntgres.Error{Code: "22023", Message: "CACHE (0) must be greater than zero"})
	diagnostic("SELECT nextval('missing')", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "16"})
	exec(a, "CREATE SEQUENCE s AS BIGINT START WITH 5 CACHE 3")
	value(a, "SELECT nextval('s')", 5)
	value(b, "SELECT nextval('s')", 8)
	value(a, "SELECT nextval('s')", 6)
	value(a, "SELECT currval('s')", 6)
	value(a, "SELECT lastval()", 6)
	prepared, err := a.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	exec(a, "ALTER SEQUENCE s RENAME TO renamed")
	var got int64
	err = prepared.QueryRowContext(ctx).Scan(&got)
	var missing *stuntgres.Error
	wantMissing := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}
	if !errors.As(err, &missing) || *missing != wantMissing {
		t.Fatalf("held source got=%#v want=%#v", missing, wantMissing)
	}
	value(a, "SELECT nextval('renamed')", 7)
	value(b, "SELECT setval('renamed',40,false)", 40)
	value(b, "SELECT nextval('renamed')", 40)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "ALTER SEQUENCE renamed RESTART WITH 70"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	value(b, "SELECT nextval('renamed')", 41)
	exec(a, "CREATE TEMP SEQUENCE private_seq START 100")
	value(a, "SELECT nextval('pg_temp.private_seq')", 100)
	if _, err = b.ExecContext(ctx, "SELECT nextval('private_seq')"); err == nil {
		t.Fatal("temporary sequence leaked")
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	replayConnector, err := stuntgres.NewDriver().OpenConnector("sequence-smoke-dump-replay")
	if err != nil {
		t.Fatal(err)
	}
	replay := sql.OpenDB(replayConnector)
	defer replay.Close()
	if err = stuntgres.Restore(ctx, replay, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var restored int64
	if err = replay.QueryRowContext(ctx, "SELECT nextval('renamed')").Scan(&restored); err != nil || restored != 43 {
		t.Fatal("persistent sequence replay", restored, err)
	}
	_, err = replay.ExecContext(ctx, "SELECT nextval('private_seq')")
	var excluded *stuntgres.Error
	if !errors.As(err, &excluded) || excluded.Code != "42P01" || excluded.Message != `relation "private_seq" does not exist` || excluded.Position != "16" {
		t.Fatalf("temporary sequence replay exclusion: %#v", excluded)
	}
	exec(a, "CREATE TABLE target(v INT)")
	exec(a, "CREATE SEQUENCE owned OWNED BY target.v")
	exec(a, "ALTER TABLE target DROP COLUMN v")
	if _, err = a.ExecContext(ctx, "SELECT nextval('owned')"); err == nil {
		t.Fatal("owned dependency survived dropped column")
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	value(a, "SELECT nextval('private_seq')", 101)
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	value(a, "SELECT nextval('renamed')", 43)
	value(a, "SELECT nextval('private_seq')", 102)
	var invalid bytes.Buffer
	fmt.Fprintln(&invalid, "CREATE SEQUENCE invalid CACHE 0;")
	if err = stuntgres.Restore(ctx, db, &invalid); err == nil {
		t.Fatal("invalid restore accepted")
	}
	value(a, "SELECT nextval('renamed')", 44)
}

func TestSequenceTemporaryPhysicalEndSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("sequence-temp-end-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxIdleConns(0)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, q := range []string{"CREATE SEQUENCE s START 40", "CREATE TEMP SEQUENCE s START 7", "BEGIN", "COMMIT"} {
		if _, err = a.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var v int64
	if err = a.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
	if err = b.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 40 {
		t.Fatal(v, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.QueryRowContext(ctx, "SELECT currval('s')").Scan(&v); err == nil {
		t.Fatal("physical session inherited memory")
	}
	if err = fresh.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 41 {
		t.Fatal("temporary shadow survived physical end", v, err)
	}
}
