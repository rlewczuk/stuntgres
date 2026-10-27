package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow PostgreSQL REL_18_6 contrib/tablefunc/tablefunc.c,
// not native output. All SQL witnesses share the same backend-independent body.
func connectbyCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "cb_part", "cb_moved", "CB Schema")
	fixture.RoleReferenceCleanup(t, []string{"cb_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS tablefunc CASCADE", "DROP SCHEMA IF EXISTS cb_part CASCADE", "DROP SCHEMA IF EXISTS cb_moved CASCADE", `DROP SCHEMA IF EXISTS "CB Schema" CASCADE`} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			connectbyRun(t, p, b, c, fixture.Exec("CREATE SCHEMA cb_part", 0))
			connectbyRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tablefunc SCHEMA cb_part", 0))
			body(t, p, b, c)
		})
	}
}

func connectbyRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}
func connectbyTree(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	connectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_tree(k text,p text,pos integer)", 0))
	connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES ('row2','ignored',0),('row5','row2',1),('row9','row5',1),('row4','row2',2),('row6','row4',1),('row8','row6',1)", 6))
}

var connectbyTreeRows = [][]any{
	{"row2", nil, int64(0), "row2", int64(1)},
	{"row5", "row2", int64(1), "row2~row5", int64(2)},
	{"row9", "row5", int64(2), "row2~row5~row9", int64(3)},
	{"row4", "row2", int64(1), "row2~row4", int64(4)},
	{"row6", "row4", int64(2), "row2~row4~row6", int64(5)},
	{"row8", "row6", int64(3), "row2~row4~row6~row8", int64(6)},
}
var connectbyDefs = []string{
	"node text,parent text,level integer",
	"node text,parent text,level integer,branch text",
	"node text,parent text,level integer,serial integer",
	"node text,parent text,level integer,branch text,serial integer",
}
var connectbyArgs = []string{
	"'cb_tree','k','p',$1::text,$2::integer",
	"'cb_tree','k','p',$1::text,$2::integer,$3::text",
	"'cb_tree','k','p','pos',$1::text,$2::integer",
	"'cb_tree','k','p','pos',$1::text,$2::integer,$3::text",
}

func connectbyQuery(variant int, args string) string {
	q := "SELECT * FROM cb_part.connectby(" + args + ") AS t(" + connectbyDefs[variant] + ")"
	if variant >= 2 {
		q += " ORDER BY serial"
	}
	return q
}
func connectbySelect(variant int, q string, rows [][]any, args ...any) fixture.Step {
	columns := []string{"node", "parent", "level"}
	types := []string{"TEXT", "TEXT", "INT4"}
	if variant == 1 || variant == 3 {
		columns = append(columns, "branch")
		types = append(types, "TEXT")
	}
	if variant >= 2 {
		columns = append(columns, "serial")
		types = append(types, "INT4")
	}
	s := fixture.Select(q, columns, types, rows, args...)
	s.Bag = variant < 2
	return s
}
func connectbyLayout(variant int, rows [][]any) [][]any {
	out := make([][]any, len(rows))
	for i, row := range rows {
		out[i] = append([]any{}, row[:3]...)
		if variant == 1 || variant == 3 {
			out[i] = append(out[i], row[3])
		}
		if variant >= 2 {
			out[i] = append(out[i], row[4])
		}
	}
	return out
}
func connectbyError(q, code, message, detail string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}
func connectbyNestedError(q, sql, code, message, position string) fixture.Step {
	s := connectbyError(q, code, message, "")
	s.Diagnostic.InternalQuery = sql
	s.Diagnostic.InternalPosition = position
	if position == "" {
		s.Diagnostic.InternalQuery = ""
		s.Diagnostic.Context = "SQL statement \"" + sql + "\""
	}
	return s
}
func connectbyHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}
func connectbyLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
