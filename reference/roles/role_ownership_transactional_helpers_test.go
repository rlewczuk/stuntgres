package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Each route owns one physical connection. In Go transactions every statement,
// including inquiries and session controls, goes through Tx rather than Conn.
var ownershipTransactionRoutes = []string{"auto", "sql-held-before", "sql-held-inside", "go-prepare", "go-stmt"}

type ownershipTransaction struct {
	s     *membershipScenario
	e     fixture.Executor
	tx    *sql.Tx
	route string
	held  map[string]*sql.Stmt
}

func ownershipBegin(s *membershipScenario, route string, queries ...string) *ownershipTransaction {
	s.t.Helper()
	x := &ownershipTransaction{s: s, e: s.c, route: route, held: map[string]*sql.Stmt{}}
	before := map[string]*sql.Stmt{}
	if s.prepared && (route == "sql-held-before" || route == "go-stmt" || route == "auto") {
		for _, q := range queries {
			stmt, err := s.c.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal("healthy utility Prepare outside transaction", q, err)
			}
			before[q] = stmt
			s.t.Cleanup(func() { stmt.Close() })
		}
	}
	switch route {
	case "auto":
	case "sql-held-before", "sql-held-inside":
		s.exec("BEGIN")
		s.t.Cleanup(func() { s.c.ExecContext(s.p.Ctx, "ROLLBACK") })
	case "go-prepare", "go-stmt":
		var err error
		x.tx, err = s.c.BeginTx(s.p.Ctx, nil)
		if err != nil {
			s.t.Fatal(err)
		}
		x.e = x.tx
		s.t.Cleanup(func() { x.tx.Rollback() })
	default:
		s.t.Fatalf("unknown ownership route %q", route)
	}
	if s.prepared {
		for _, q := range queries {
			stmt := before[q]
			if x.tx != nil && stmt != nil {
				stmt = x.tx.StmtContext(s.p.Ctx, stmt)
			} else if stmt == nil {
				var err error
				if x.tx != nil {
					stmt, err = x.tx.PrepareContext(s.p.Ctx, q)
				} else {
					stmt, err = s.c.PrepareContext(s.p.Ctx, q)
				}
				if err != nil {
					s.t.Fatal("healthy utility Prepare inside transaction", q, err)
				}
			}
			x.held[q] = stmt
			s.t.Cleanup(func() { stmt.Close() })
			// NumInput is exposed by database/sql even though Stmt has no NumInput API.
			result, err := stmt.ExecContext(s.p.Ctx, 1)
			if result != nil || err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
				s.t.Fatal("utility must have zero parameters and no effect", result, err)
			}
		}
	}
	return x
}

func (x *ownershipTransaction) run(step fixture.Step) {
	x.s.t.Helper()
	var e fixture.Executor = x.e
	if stmt := x.held[step.Query]; stmt != nil {
		e = fixture.HeldStatement{Executor: x.e, Statement: stmt, SQL: step.Query}
	}
	fixture.RunStep(x.s.t, x.s.p.Ctx, e, x.s.record, step)
}
func (x *ownershipTransaction) exec(q string) { x.s.t.Helper(); x.run(fixture.Exec(q, 0)) }
func (x *ownershipTransaction) end(commit bool) {
	x.s.t.Helper()
	for _, stmt := range x.held {
		stmt.Close()
	}
	if x.route == "auto" {
		return
	}
	if x.tx != nil {
		var err error
		if commit {
			err = x.tx.Commit()
		} else {
			err = x.tx.Rollback()
		}
		if err != nil {
			x.s.t.Fatal(err)
		}
	} else if commit {
		x.exec("COMMIT")
	} else {
		x.exec("ROLLBACK")
	}
}
func (x *ownershipTransaction) abort() {
	for _, stmt := range x.held {
		stmt.Close()
	}
	if x.tx != nil {
		x.tx.Rollback()
	} else if x.route != "auto" {
		x.e.ExecContext(x.s.p.Ctx, "ROLLBACK")
	}
}

func ownershipTransactionCases(t *testing.T, body func(*membershipScenario, string)) {
	t.Helper()
	for _, route := range ownershipTransactionRoutes {
		t.Run(route, func(t *testing.T) { ownershipCases(t, func(s *membershipScenario) { body(s, route) }) })
	}
}
