package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgRangeConsume(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var a, b, c, d, e int64
		var f, g string
		if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{a, b, c, d, e, f, g})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained range rows got %v want %v", got, want)
	}
}
func pgRangeHeldExecution(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, pgRangeProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgRangeProbe}
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_range ORDER BY rngtypid")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgRangeMetadata(t, old, b.PG)
	run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
	run("CREATE TABLE pgrange_held(a int4range)")
	run("CREATE DOMAIN pgrange_held_domain AS int4range")
	for _, value := range []any{int64(3904), int64(3908), int64(0), nil, int64(3926)} {
		pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(value), false, value)
	}
	run("DROP TABLE pgrange_held")
	run("DROP DOMAIN pgrange_held_domain")
	pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(int64(3904)), false, int64(3904))
	pgRangeMetadata(t, old, b.PG)
	pgRangeConsume(t, old, pgRangeFacts())
	pgRangeInventory(t, p, b)
}
func TestReferencePGRangeHeldExecution(t *testing.T) { pgRangeScenario(t, pgRangeHeldExecution) }

func pgRangeLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE pgrange_life(n INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO pgrange_life VALUES(7,'kept'),(8,'also')", 2))
	run(fixture.Exec("CREATE VIEW pgrange_life_view AS SELECT rngtypid,rngsubtype,rngmultitypid FROM pg_catalog.pg_range", 0))
	stmt, err := b.DB.PrepareContext(p.Ctx, pgRangeProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgRangeProbe}
	viewq := "SELECT rngtypid,rngsubtype,rngmultitypid FROM pgrange_life_view ORDER BY rngtypid"
	viewstmt, err := b.DB.PrepareContext(p.Ctx, viewq)
	if err != nil {
		t.Fatal(err)
	}
	defer viewstmt.Close()
	viewheld := fixture.HeldStatement{Executor: b.DB, Statement: viewstmt, SQL: viewq}
	check := func() {
		pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(int64(3908)), false, int64(3908))
		var want [][]any
		for _, r := range pgRangeNumericFacts() {
			want = append(want, append([]any(nil), r[:3]...))
		}
		s := fixture.Select(viewq, pgRangeColumns[:3], []string{"OID", "OID", "OID"}, want)
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		fixture.PgDescriptionExecutorRows(t, p, b, viewheld, s)
		run(fixture.Select("SELECT n,s FROM pgrange_life ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), "also"}}))
	}
	check()
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_range ORDER BY rngtypid")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgRangeMetadata(t, old, b.PG)
	if !b.PG {
		// Controls are explicitly native-only; shared SQL assertions stay above.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE VIEW") || !strings.Contains(dump.String(), "pgrange_life_view") || !strings.Contains(dump.String(), "INSERT INTO") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_range") {
			t.Fatal("not a user-only logical dump", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgRangeInventory(t, p, b)
		pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(int64(3904)), false, int64(3904))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pgrange_life','pgrange_life_view')", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check()
		pgRangeInventory(t, p, b)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if again.String() != dump.String() {
			t.Fatalf("redump changed\nbefore:%s\nafter:%s", dump.String(), again.String())
		}
	}
	pgRangeMetadata(t, old, b.PG)
	pgRangeConsume(t, old, pgRangeFacts())
}
func TestReferencePGRangeLifecycle(t *testing.T) { pgRangeScenario(t, pgRangeLifecycle) }
func pgRangeCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgRangeRows(t, p, b, b.DB, pgRangeProbe, pgRangeLookup(int64(3904)), false, int64(3904))
	if b.PG {
		return
	} // pq scheduling is not a server SQL diagnostic contract.
	stmt, err := b.DB.PrepareContext(p.Ctx, pgRangeProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	ordinary := func(err error) {
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation gained SQLSTATE", err)
		}
	}
	rows, err := b.DB.QueryContext(ctx, pgRangeProbe, int64(3904))
	if rows != nil {
		rows.Close()
		t.Fatal("canceled direct published rows")
	}
	ordinary(err)
	rows, err = stmt.QueryContext(ctx, int64(3904))
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held published rows")
	}
	ordinary(err)
	s, err := b.DB.PrepareContext(ctx, pgRangeProbe)
	if s != nil {
		s.Close()
		t.Fatal("canceled prepare published statement")
	}
	ordinary(err)
	pgRangeRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgRangeProbe}, pgRangeProbe, pgRangeLookup(int64(3926)), false, int64(3926))
}
func TestReferencePGRangeCancellation(t *testing.T) { pgRangeScenario(t, pgRangeCancellation) }
func TestReferencePGRangeFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgrange_reader"})
	pgRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Compose feature bodies, not duplicate smoke assertions or top-level tests.
		pgRangeInventory(t, p, b)
		pgRangeReads(t, p, b, b.DB)
		pgRangeLinkage(t, p, b)
		pgRangeSemantics(t, p, b)
		pgRangeMissing(t, p, b)
		pgRangeProtected(t, p, b)
		pgRangePermissions(t, p, b)
		pgRangeLifecycle(t, p, b)
	})
}
