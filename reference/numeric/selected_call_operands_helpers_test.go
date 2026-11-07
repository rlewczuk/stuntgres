package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func selectedCallScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func selectedCallRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func selectedCallBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%v", s.Query, prepared), func(t *testing.T) {
			step := s
			step.Prepared = prepared
			selectedCallRun(t, p, b, e, step)
		})
	}
}

func selectedCallHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, body func(fixture.Executor)) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	body(fixture.HeldStatement{Executor: c, Statement: s, SQL: q})
}

func selectedCallReadModes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name, typ string, check func(*testing.T, any)) {
	t.Helper()
	read := func(t *testing.T, e fixture.Executor) {
		v := randomScalar(t, p, e, b, q, name, typ)
		check(t, v)
	}
	t.Run("direct/"+q, func(t *testing.T) { read(t, c) })
	t.Run("prepared/"+q, func(t *testing.T) {
		selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) { read(t, e) })
	})
}

func selectedCallRange(t *testing.T, value any, upper float64) {
	t.Helper()
	v, ok := value.(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v >= upper {
		t.Fatalf("value=%#v; want finite FLOAT8 in [0,%g)", value, upper)
	}
}

func selectedCallFloat(t *testing.T, value any, want float64, ulps uint64) {
	t.Helper()
	got, ok := value.(float64)
	if !ok || !referenceScalarFloatMatches(got, want, ulps) {
		t.Fatalf("FLOAT8 got %#v, want %.17g within %d ULP", value, want, ulps)
	}
}

var selectedCallStampPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Consumes and records nondeterministic bytes once, then checks independent
// shape, identities and row-local freshness rather than cross-engine equality.
func selectedCallStamps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []string {
	t.Helper()
	q := "SELECT id,security_stamp FROM users ORDER BY id"
	rows := randomRead(t, p.Ctx, c, b, q, []string{"id", "security_stamp"}, []string{"INT4", "TEXT"}, nil)
	if len(rows) != 3 {
		t.Fatalf("users row count: %#v", rows)
	}
	stamps := make([]string, 3)
	for i, row := range rows {
		if row[0] != int64(i+1) {
			t.Fatalf("users identities: %#v", rows)
		}
		var ok bool
		stamps[i], ok = row[1].(string)
		if !ok {
			t.Fatalf("stamp type: %#v", row[1])
		}
	}
	return stamps
}

func selectedCallChangedStamps(t *testing.T, before, after []string, changed ...int) {
	t.Helper()
	seen := make(map[string]bool)
	for i, got := range after {
		change := false
		for _, id := range changed {
			if id == i+1 {
				change = true
			}
		}
		if change {
			if !selectedCallStampPattern.MatchString(got) || got == before[i] || seen[got] {
				t.Fatalf("row %d stamp=%q before=%q; want fresh distinct lowercase32 hex", i+1, got, before[i])
			}
			seen[got] = true
		} else if got != before[i] {
			t.Fatalf("unselected row %d changed from %q to %q", i+1, before[i], got)
		}
	}
}

func selectedCallNumber(value any) string { return fmt.Sprint(value) }

// A driver may surface execution failure at Query or at the first Rows.Next.
// Neither route may expose a successful row; Prepare must itself succeed.
func selectedCallRuntimeError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, prepared bool, args ...any) {
	t.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	run := func(e fixture.Executor) {
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
		rows, err := e.QueryContext(ctx, q, args...)
		if err != nil {
			if rows != nil {
				rows.Close()
				t.Fatal("failed Query exposed a cursor", q)
			}
		} else {
			if rows == nil {
				t.Fatal("Query returned neither cursor nor error", q)
			}
			defer rows.Close()
			if rows.Next() {
				t.Fatal("failing expression published a successful row", q)
			}
			err = rows.Err()
			// On execution failure Close may repeat the same terminal error;
			// the complete provider diagnostic below remains authoritative.
			closeErr := rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		fixture.AssertDiagnostic(t, err, &d)
	}
	if prepared {
		selectedCallHeld(t, p, b, c, q, run)
	} else {
		run(c)
	}
}
