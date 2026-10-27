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
	s     *fixture.RoleMembershipScenario
	e     fixture.Executor
	tx    *sql.Tx
	route string
	held  map[string]*sql.Stmt
}

func ownershipBegin(s *fixture.RoleMembershipScenario, route string, queries ...string) *ownershipTransaction {
	s.T.Helper()
	x := &ownershipTransaction{s: s, e: s.C, route: route, held: map[string]*sql.Stmt{}}
	before := map[string]*sql.Stmt{}
	if s.Prepared && (route == "sql-held-before" || route == "go-stmt" || route == "auto") {
		for _, q := range queries {
			stmt, err := s.C.PrepareContext(s.P.Ctx, q)
			if err != nil {
				s.T.Fatal("healthy utility Prepare outside transaction", q, err)
			}
			before[q] = stmt
			s.T.Cleanup(func() { stmt.Close() })
		}
	}
	switch route {
	case "auto":
	case "sql-held-before", "sql-held-inside":
		s.Exec("BEGIN")
		s.T.Cleanup(func() { s.C.ExecContext(s.P.Ctx, "ROLLBACK") })
	case "go-prepare", "go-stmt":
		var err error
		x.tx, err = s.C.BeginTx(s.P.Ctx, nil)
		if err != nil {
			s.T.Fatal(err)
		}
		x.e = x.tx
		s.T.Cleanup(func() { x.tx.Rollback() })
	default:
		s.T.Fatalf("unknown ownership route %q", route)
	}
	if s.Prepared {
		for _, q := range queries {
			stmt := before[q]
			if x.tx != nil && stmt != nil {
				stmt = x.tx.StmtContext(s.P.Ctx, stmt)
			} else if stmt == nil {
				var err error
				if x.tx != nil {
					stmt, err = x.tx.PrepareContext(s.P.Ctx, q)
				} else {
					stmt, err = s.C.PrepareContext(s.P.Ctx, q)
				}
				if err != nil {
					s.T.Fatal("healthy utility Prepare inside transaction", q, err)
				}
			}
			x.held[q] = stmt
			s.T.Cleanup(func() { stmt.Close() })
			// NumInput is exposed by database/sql even though Stmt has no NumInput API.
			result, err := stmt.ExecContext(s.P.Ctx, 1)
			if result != nil || err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
				s.T.Fatal("utility must have zero parameters and no effect", result, err)
			}
		}
	}
	return x
}

func (x *ownershipTransaction) run(step fixture.Step) {
	x.s.T.Helper()
	var e fixture.Executor = x.e
	if stmt := x.held[step.Query]; stmt != nil {
		e = fixture.HeldStatement{Executor: x.e, Statement: stmt, SQL: step.Query}
	}
	fixture.RunStep(x.s.T, x.s.P.Ctx, e, x.s.Record, step)
}
func (x *ownershipTransaction) exec(q string) { x.s.T.Helper(); x.run(fixture.Exec(q, 0)) }
func (x *ownershipTransaction) end(commit bool) {
	x.s.T.Helper()
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
			x.s.T.Fatal(err)
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
		x.e.ExecContext(x.s.P.Ctx, "ROLLBACK")
	}
}

func ownershipTransactionCases(t *testing.T, body func(*fixture.RoleMembershipScenario, string)) {
	t.Helper()
	for _, route := range ownershipTransactionRoutes {
		t.Run(route, func(t *testing.T) {
			ownershipCasesFor(t, ownershipRouteModes(route), func(s *fixture.RoleMembershipScenario) { body(s, route) })
		})
	}
}

// ownershipRouteModes lists the prepared modes a route adds coverage in.
// Without held statements sql-held-inside runs exactly like sql-held-before
// (SQL BEGIN) and go-stmt like go-prepare (BeginTx), so they run prepared only.
func ownershipRouteModes(route string) []bool {
	if route == "sql-held-inside" || route == "go-stmt" {
		return []bool{true}
	}
	return []bool{false, true}
}
