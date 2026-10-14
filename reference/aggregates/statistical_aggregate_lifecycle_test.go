package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticalAtomicity(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, r, s) }
	run(fixture.Exec("CREATE TABLE bad_stats(g INTEGER,v DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO bad_stats VALUES (1,1),(1,3),(2,1e200),(2,0)", 4))
	run(fixture.Exec("CREATE TABLE stats_sentinel(g INTEGER,v DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO stats_sentinel VALUES (99,9)", 1))
	q := "SELECT g,var_pop(v) FROM bad_stats GROUP BY g ORDER BY g"
	statisticalExecutionError(t, p, conn, r, q, "value out of range: overflow", true)
	statisticalExecutionError(t, p, conn, r, "INSERT INTO stats_sentinel "+q, "value out of range: overflow", false)
	run(fixture.Select("SELECT g,v FROM stats_sentinel", []string{"g", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(9)}}))
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "value out of range: overflow"})
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, r, s)
	run(fixture.Exec("DELETE FROM bad_stats WHERE g=2", 2))
	fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"g", "var_pop"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(1)}}))
}

func statisticalPersistent(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, r, s) }
	run(fixture.Exec("CREATE TABLE stats_source(k INTEGER,n NUMERIC,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO stats_source VALUES (1,1,2,1),(2,2,4,2),(3,3,6,3),(4,NULL,NULL,1000),(5,NULL,1000,NULL)", 5))
	q := "SELECT regr_slope(y,x) AS slope,regr_sxx(y,x) AS xx,regr_sxy(y,x) AS xy,regr_syy(y,x) AS yy,var_samp(n) AS v,stddev(n) AS s,variance(n) AS a,var_pop(x) FILTER (WHERE y IS NOT NULL) AS f FROM stats_source"
	columns := []string{"slope", "xx", "xy", "yy", "v", "s", "a", "f"}
	types := []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC", "FLOAT8"}
	values := []any{float64(2), float64(2), float64(4), float64(8), "1.00000000000000000000", "1.00000000000000000000", "1.00000000000000000000", float64(2.0 / 3)}
	s := fixture.Select(q, columns, types, [][]any{values})
	s.CheckMetadata = statisticalMetadata(pg, types)
	statisticalBoth(t, p, conn, r, s)
	parameterQ := "SELECT regr_slope($1::double precision*x,$2::double precision*x),var_samp($3::numeric*n) FROM stats_source WHERE k<=3"
	stmt, err := conn.PrepareContext(p.Ctx, parameterQ)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: parameterQ}
		for _, c := range []struct {
			y, x, n  float64
			slope    float64
			variance string
		}{{2, 1, 1, 2, "1.00000000000000000000"}, {6, 2, 2, 3, "4.0000000000000000"}} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(parameterQ, []string{"regr_slope", "var_samp"}, []string{"FLOAT8", "NUMERIC"}, [][]any{{c.slope, c.variance}}, c.y, c.x, c.n))
		}
	}()
	run(fixture.Exec("CREATE VIEW stats_view AS "+q, 0))
	run(fixture.Exec("CREATE TABLE stats_result AS SELECT * FROM stats_view", 1))
	for _, table := range []string{"stats_view", "stats_result"} {
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT slope,xx,xy,yy,v,s,a,f FROM "+table, columns, types, [][]any{values}))
	}
	if pg {
		return
	}
	rows, err := conn.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	statisticalMetadata(false, types)(t, rows)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stats_view", "stats_result"} {
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT slope,xx,xy,yy,v,s,a,f FROM "+table, columns, types, [][]any{values}))
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Error("canonical moment persistence changed after restore")
	}
	statisticalMetadata(false, types)(t, rows)
	if !rows.Next() {
		t.Fatalf("retained row: %v", rows.Err())
	}
	got := make([]any, len(values))
	destinations := make([]any, len(values))
	for i := range got {
		destinations[i] = &got[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		t.Fatal(err)
	}
	for i := range values {
		if got[i] != values[i] {
			t.Errorf("retained column %d: %v want %v", i, got[i], values[i])
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("retained completion: %v", rows.Err())
	}
	rows.Close()
	for _, prepared := range []bool{false, true} {
		query := "INSERT INTO stats_result " + q
		var statement *sql.Stmt
		var selectStatement *sql.Stmt
		if prepared {
			statement, err = conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			selectStatement, err = conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer selectStatement.Close()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		var canceledRows *sql.Rows
		if prepared {
			canceledRows, err = selectStatement.QueryContext(ctx)
		} else {
			canceledRows, err = conn.QueryContext(ctx, q)
		}
		if canceledRows != nil {
			canceledRows.Close()
			t.Error("canceled moments published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled moment SELECT: %v", err)
		}
		var result sql.Result
		if prepared {
			result, err = statement.ExecContext(ctx)
		} else {
			result, err = conn.ExecContext(ctx, query)
		}
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled moment INSERT: %v/%v", result, err)
		}
		if _, ok := err.(interface{ SQLState() string }); ok {
			t.Errorf("ordinary cancellation has SQLSTATE: %v", err)
		}
		run(fixture.Select("SELECT slope,xx,xy,yy,v,s,a,f FROM stats_result", columns, types, [][]any{values}))
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("invalid native fixture")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore: %v", err)
	}
	var retained bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &retained); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), retained.Bytes()) {
		t.Fatal("failed Restore changed persistent moments")
	}
}

func TestReferenceStatisticalAggregateLifecycle(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
		statisticalPersistent(t, p, conn, r, pg)
		statisticalAtomicity(t, p, conn, r)
	})
}
