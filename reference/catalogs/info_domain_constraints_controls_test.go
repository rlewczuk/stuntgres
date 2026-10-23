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

func dcIdentity(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	var want [][]any
	for i, name := range dcColumns {
		domain := "sql_identifier"
		if i >= 6 {
			domain = "yes_or_no"
		}
		want = append(want, []any{name, "information_schema", domain})
	}
	fixture.RunStep(t, p.Ctx, x, r, fixture.Select(`SELECT a.attname,n.nspname,t.typname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.domain_constraints'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attname", "nspname", "typname"}, []string{"NAME", "NAME", "NAME"}, want))
	fixture.RunStep(t, p.Ctx, x, r, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.domain_constraints'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
}

func dcRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	count := 0
	for rows.Next() {
		var cells [8]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6], &cells[7]); err != nil {
			t.Fatal(err)
		}
		matched := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(cells[:], expected) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected retained row %v", cells)
		}
		count++
	}
	if rows.Err() != nil || count != len(want) {
		t.Fatalf("retained count=%d want=%d error=%v", count, len(want), rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceInfoDomainConstraintsNativeControls(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		dcSeed(run)
		s := dcProjection(b.PG, "domain_schema='dc_scope'", dcSeedRows(database))
		held := dcHold(t, p, c, s.Query)
		dcCheck(t, p, c, r, held, s)
		dcIdentity(t, p, c, r)
		if b.PG {
			return // PostgreSQL has no process-local Reset/Dump/Restore API.
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		dcMetadata(t, columns, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(dcProjection(false, "domain_schema='dc_scope'", nil))
		run(dcProjection(false, "domain_schema='information_schema' AND domain_name IN ('cardinal_number','yes_or_no')", [][]any{
			dcRow(database, "information_schema", "cardinal_number_domain_check", "cardinal_number"),
			dcRow(database, "information_schema", "yes_or_no_check", "yes_or_no"),
		}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		// Fresh preparation after replacement, not a promise of retained analysis.
		run(s)
		run(fixture.Prepared(s))
		dcIdentity(t, p, c, r)
		dcRetained(t, retained, dcSeedRows(database))
		dcMetadata(t, columns, false)
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var targetDatabase string
		if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&targetDatabase); err != nil {
			t.Fatal(err)
		}
		targetStep := dcProjection(false, "domain_schema='dc_scope'", dcSeedRows(targetDatabase))
		checkTarget := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, targetStep)
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(targetStep))
			dcIdentity(t, target, target.Memory, target.MemoryRecord)
		}
		checkTarget()
		bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t(v staged.d); INSERT INTO staged.t VALUES(-1); ALTER DOMAIN staged.d ADD CONSTRAINT positive CHECK(missing>0) NOT VALID;"
		fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), "42703")
		checkTarget()
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname='staged'", []string{"nspname"}, []string{"NAME"}, nil))
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		checkTarget()
		ctx, cancel = context.WithCancel(p.Ctx)
		cancel()
		freshHeld := dcHold(t, p, c, s.Query)
		for _, x := range []fixture.Executor{c, freshHeld} {
			rows, err := x.QueryContext(ctx, s.Query)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled query=%v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
			}
		}
		run(s)
		run(fixture.Prepared(s))
	})
}

func TestReferenceInfoDomainConstraintsFeatureSmoke(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		dcSeed(run)
		where := "domain_schema='dc_scope'"
		held := dcHold(t, p, c, dcProjection(b.PG, where, nil).Query)
		check := func(want [][]any) { dcCheck(t, p, c, r, held, dcProjection(b.PG, where, want)) }
		check(dcSeedRows(database))
		dcIdentity(t, p, c, r)
		smoke := dcRow(database, "dc_scope", "smoke", "a")
		four := append(dcSeedRows(database), smoke)
		run(fixture.Exec("ALTER DOMAIN dc_scope.a ADD CONSTRAINT smoke CHECK(VALUE<100) NOT VALID", 0))
		check(four)
		run(dcFlags("a", "smoke", false))
		run(fixture.Exec("ALTER DOMAIN dc_scope.a VALIDATE CONSTRAINT smoke", 0))
		check(four)
		run(dcFlags("a", "smoke", true))
		run(fixture.Exec("ALTER DOMAIN dc_scope.a DROP CONSTRAINT same", 0))
		survivors := [][]any{dcRow(database, "dc_scope", "same", "b"), dcRow(database, "dc_scope", "named_nn", "nn")}
		check(append(append([][]any(nil), survivors...), smoke))
		run(fixture.Exec("CREATE ROLE dc_reader", 0))
		run(fixture.Exec("REVOKE ALL ON SCHEMA dc_scope FROM PUBLIC", 0))
		run(fixture.Exec("REVOKE USAGE ON DOMAIN dc_scope.a FROM PUBLIC", 0))
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		run(fixture.Exec("SET SESSION AUTHORIZATION dc_reader", 0))
		check(survivors)
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("GRANT USAGE ON DOMAIN dc_scope.a TO dc_reader", 0))
		check(append(append([][]any(nil), survivors...), smoke))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, s := range []fixture.Step{
			dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name IS NULL", nil),
			dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name=$1", nil),
		} {
			if strings.Contains(s.Query, "$1") {
				s.Args = []any{nil}
			}
			run(s)
			run(fixture.Prepared(s))
		}
		dcMissing(run)
	})
}
