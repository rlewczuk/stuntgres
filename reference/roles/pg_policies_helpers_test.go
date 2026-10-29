package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

var pgPoliciesColumns = []string{"schemaname", "tablename", "policyname", "permissive", "roles", "cmd", "qual", "with_check"}
var pgPoliciesTypes = []string{"NAME", "NAME", "NAME", "TEXT", "_NAME", "TEXT", "TEXT", "TEXT"}

const pgPoliciesQuery = "SELECT * FROM pg_catalog.pg_policies WHERE schemaname='public' ORDER BY tablename,policyname"

// Use the shared role/session scenario and diagnostic conventions.
func pgPoliciesCases(t *testing.T, names []string, body func(*fixture.RoleMembershipScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					bootstrap := "postgres"
					if backend.PG {
						bootstrap = "stuntgres"
					}
					s := &fixture.RoleMembershipScenario{T: t, P: p, Backend: backend, C: c, Record: record, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					for _, name := range names {
						s.Exec("CREATE ROLE " + pq.QuoteIdentifier(name) + " LOGIN")
					}
					body(s)
				})
			}
		})
	}
}

func pgPoliciesRow(table, policy, mode, roles, command string, using, check any) []any {
	return []any{"public", table, policy, mode, roles, command, using, check}
}

func pgPoliciesSelect(query string, rows [][]any) fixture.Step {
	s := fixture.Select(query, pgPoliciesColumns, pgPoliciesTypes, rows)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 8 {
			t.Fatalf("column count %d", len(types))
		}
		for i, ct := range types {
			if ct.Name() != pgPoliciesColumns[i] || ct.DatabaseTypeName() != pgPoliciesTypes[i] {
				t.Fatalf("column %d: %s %s", i, ct.Name(), ct.DatabaseTypeName())
			}
			if nullable, known := ct.Nullable(); known && !nullable {
				t.Fatalf("view column %s unexpectedly NOT NULL", ct.Name())
			}
		}
	}
	return s
}

func pgPoliciesRead(s *fixture.RoleMembershipScenario, rows ...[]any) {
	s.T.Helper()
	if rows == nil {
		rows = [][]any{}
	}
	s.Run(pgPoliciesSelect(pgPoliciesQuery, rows))
}

func pgPoliciesHeld(s *fixture.RoleMembershipScenario, query string) func(fixture.Step) {
	s.T.Helper()
	stmt, err := s.C.PrepareContext(s.P.Ctx, query)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() { if err := stmt.Close(); err != nil { s.T.Error(err) } })
	held := fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: query}
	return func(step fixture.Step) {
		s.T.Helper()
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
	}
}

// Retain genuine adapter values and column descriptors, not just a second query.
func pgPoliciesSnapshot(s *fixture.RoleMembershipScenario) func() {
	s.T.Helper()
	rows, err := s.C.QueryContext(s.P.Ctx, pgPoliciesQuery)
	if err != nil { s.T.Fatal(err) }
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil { s.T.Fatal(err) }
	types, err := rows.ColumnTypes()
	if err != nil { s.T.Fatal(err) }
	var saved [][]any
	for rows.Next() {
		values := make([]any, 8)
		dest := make([]any, 8)
		for i := range values { dest[i] = &values[i] }
		if err := rows.Scan(dest...); err != nil { s.T.Fatal(err) }
		saved = append(saved, values)
	}
	if err := rows.Err(); err != nil { s.T.Fatal(err) }
	want := make([][]any, len(saved))
	for i, row := range saved {
		want[i] = make([]any, len(row))
		for j, value := range row { want[i][j] = fixture.Normalize(value) }
	}
	return func() {
		s.T.Helper()
		if !reflect.DeepEqual(columns, pgPoliciesColumns) { s.T.Fatal("retained columns", columns) }
		for i, ct := range types {
			if ct.Name() != pgPoliciesColumns[i] || ct.DatabaseTypeName() != pgPoliciesTypes[i] { s.T.Fatal("retained descriptor", i, ct) }
		}
		for i, row := range saved {
			for j, value := range row {
				if !reflect.DeepEqual(fixture.Normalize(value), want[i][j]) { s.T.Fatal("retained value changed", i, j, value, want[i][j]) }
			}
		}
	}
}
