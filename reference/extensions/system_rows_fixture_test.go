package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var srNames = []string{"id", "payload"}
var srTypes = []string{"INT4", "TEXT"}

// Backend selection precedes all SQL setup, so /postgres is an oracle-first
// selector even when the native parser cannot yet accept TABLESAMPLE.
func srScenario(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "sr_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET search_path", "DROP EXTENSION IF EXISTS tsm_system_rows CASCADE", "DROP SCHEMA IF EXISTS sr_moved CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}()
			if install {
				srRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tsm_system_rows", 0))
			}
			srRun(t, p, b, c, fixture.Exec("CREATE TABLE sr(id integer NOT NULL,payload text)", 0))
			srRun(t, p, b, c, fixture.Exec("CREATE TABLE sr_empty(id integer NOT NULL,payload text)", 0))
			srRun(t, p, b, c, fixture.Exec("INSERT INTO sr VALUES(1,'v1'),(2,'v2'),(3,'v3'),(4,'v4'),(5,'v5'),(6,'v6'),(7,'v7'),(8,'v8'),(9,'v9'),(10,'v10')", 10))
			body(t, p, b, c)
		})
	}
}

func srCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	srScenario(t, true, body)
}

func srRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func srBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	srRun(t, p, b, c, s)
	srRun(t, p, b, c, fixture.Prepared(s))
}

func srCount(q string, n int64, args ...any) fixture.Step {
	return fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, args...)
}

// Only deterministic properties enter the recorder: arbitrary sampled IDs must
// never become parity goldens or imply a PostgreSQL heap traversal order.
func srSubset(table, method, argument string, n int64, args ...any) fixture.Step {
	q := "SELECT count(*) AS n,count(DISTINCT id) AS unique_ids,COALESCE(bool_and(id BETWEEN 1 AND 10 AND payload='v'||id::text),true) AS valid FROM " + table + " TABLESAMPLE " + method + "(" + argument + ")"
	return fixture.Select(q, []string{"n", "unique_ids", "valid"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{n, n, true}}, args...)
}

func srRows() [][]any {
	rows := make([][]any, 10)
	for i := range rows {
		rows[i] = []any{int64(i + 1), fmt.Sprintf("v%d", i+1)}
	}
	return rows
}

func srMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.TableDDLColumnTypes(t, pg, columns, srNames, srTypes, []bool{false, true})
	}
	return s
}

func srError(q, code, message string, position int, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Position: fixture.Position(position)}, args...)
	s.SelectRows = true
	return s
}

func srHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
}
