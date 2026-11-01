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

func vcuRetained(t *testing.T, rows *sql.Rows, columns []*sql.ColumnType, want [][]any) {
	t.Helper()
	defer rows.Close()
	var retained [][]any
	for rows.Next() {
		var values [7]any
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if _, ok := value.(string); !ok {
				t.Fatalf("retained column %d=%T(%v)", i, value, value)
			}
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
	vcuMetadata(t, columns, false)
}

// Controls are explicitly native-only phases after the caller's independently
// expected SQL witnesses have run on both backends. The provider is reconstructed
// from stored definitions/owners, never serialized as rows or user DDL.
func vcuRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, where string, want [][]any, mutate, check func()) {
	t.Helper()
	if b.PG {
		return
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	capture := func() (*sql.Rows, []*sql.ColumnType) {
		rows, err := c.QueryContext(p.Ctx, vcuProjection(false, where, want).Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rows.Close() })
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		vcuMetadata(t, columns, false)
		return rows, columns
	}
	rows, columns := capture()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"CREATE VIEW information_schema.view_column_usage", "INSERT INTO information_schema.view_column_usage",
		`CREATE VIEW "information_schema"."view_column_usage"`, `INSERT INTO "information_schema"."view_column_usage"`,
	} {
		if strings.Contains(dump.String(), forbidden) {
			t.Fatalf("dump materialized provider: %s", forbidden)
		}
	}
	if mutate != nil {
		mutate()
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(vcuProjection(false, where, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	vcuRetained(t, rows, columns, want)
	check()
	// A late failure must discard even valid staged definitions and leave
	// the pre-existing full bag, named owners and retained cursor untouched.
	rows, columns = capture()
	bad := "CREATE TABLE vcu_staged(a integer); CREATE VIEW vcu_staged_v AS SELECT a FROM vcu_staged; CREATE VIEW vcu_bad AS SELECT a FROM vcu_absent;"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	check()
	staged := fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('vcu_staged','vcu_staged_v','vcu_bad')", []string{"relname"}, []string{"NAME"}, nil)
	run(staged)
	run(fixture.Prepared(staged))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore=%v", err)
	}
	check()
	canceledRows, err := c.QueryContext(ctx, vcuProjection(false, where, want).Query)
	if canceledRows != nil {
		_ = canceledRows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	check()
	vcuRetained(t, rows, columns, want)
}

func TestReferenceInfoViewColumnUsageControls(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vcuSeed(run)
		run(fixture.Exec("CREATE ROLE vcu_b", 0))
		run(fixture.Exec("CREATE ROLE vcu_reader", 0))
		run(fixture.Exec("ALTER TABLE vcu_scope.t OWNER TO vcu_b", 0))
		where := "view_schema='vcu_scope'"
		check := func() {
			s := vcuProjection(b.PG, where, vcuSeedRows(database))
			run(s)
			run(fixture.Prepared(s))
			for _, actor := range []struct {
				role string
				rows [][]any
			}{
				{"vcu_b", [][]any{vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "a"), vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "b")}},
				{"vcu_reader", nil},
			} {
				run(fixture.Exec("SET SESSION AUTHORIZATION "+actor.role, 0))
				s := vcuProjection(b.PG, where, actor.rows)
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		check()
		mutate := func() {
			run(fixture.Exec("CREATE OR REPLACE VIEW vcu_scope.v AS SELECT a,a AS b,a AS again FROM vcu_scope.u", 0))
			s := vcuProjection(false, where, [][]any{vcuRow(database, "vcu_scope", "v", "vcu_scope", "u", "a"), vcuRow(database, "vcu_scope", "w", "vcu_scope", "v", "a")})
			run(s)
			run(fixture.Prepared(s))
		}
		vcuRoundTrip(t, p, b, c, r, where, vcuSeedRows(database), mutate, check)
	})
}
