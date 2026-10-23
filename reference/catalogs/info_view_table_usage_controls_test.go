package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native controls have no PostgreSQL counterpart. The caller first exercises
// its independent SQL seed and expectations on every available backend.
func vtuRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, where string, want [][]any, check func()) {
	t.Helper()
	if b.PG {
		return
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	rows, err := c.QueryContext(p.Ctx, vtuProjection(false, where, want).Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	vtuMetadata(t, columns, false)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"CREATE VIEW information_schema.view_table_usage", "INSERT INTO information_schema.view_table_usage",
		`CREATE VIEW "information_schema"."view_table_usage"`, `INSERT INTO "information_schema"."view_table_usage"`,
	} {
		if strings.Contains(dump.String(), forbidden) {
			t.Fatalf("dump materialized provider: %s", forbidden)
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(vtuProjection(false, where, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var retained [][]any
	for rows.Next() {
		var values [6]any
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5]); err != nil {
			t.Fatal(err)
		}
		retained = append(retained, append([]any(nil), values[:]...))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(retained) != len(want) {
		t.Fatalf("retained=%v want=%v", retained, want)
	}
	for _, expected := range want {
		found := 0
		for _, actual := range retained {
			if reflect.DeepEqual(actual, expected) {
				found++
			}
		}
		if found != 1 {
			t.Fatalf("retained=%v missing/duplicate=%v", retained, expected)
		}
	}
	vtuMetadata(t, columns, false)
	check()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE VIEW vtu_bad AS SELECT a FROM vtu_absent;")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	check()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore=%v", err)
	}
	check()
	canceledRows, err := c.QueryContext(ctx, vtuProjection(false, where, want).Query)
	if canceledRows != nil {
		_ = canceledRows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	check()
}

func TestReferenceInfoViewTableUsageControls(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vtuSeed(run)
		run(fixture.Exec("CREATE ROLE vtu_b", 0))
		run(fixture.Exec("CREATE ROLE vtu_reader", 0))
		run(fixture.Exec("ALTER TABLE vtu_scope.t OWNER TO vtu_b", 0))
		where := "view_schema='vtu_scope'"
		check := func() {
			s := vtuProjection(b.PG, where, vtuSeedRows(database))
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("SET SESSION AUTHORIZATION vtu_b", 0))
			run(vtuProjection(b.PG, where, [][]any{vtuRow(database, "vtu_scope", "v", "vtu_scope", "t")}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION vtu_reader", 0))
			run(vtuProjection(b.PG, where, nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		check()
		vtuRoundTrip(t, p, b, c, r, where, vtuSeedRows(database), check)
	})
}
