package fixture

import (
	"database/sql"
	"strings"
	"testing"
)

func ConnectbyRun(t *testing.T, p *Pair, b Backend, x Executor, s Step) {
	t.Helper()
	RunStep(t, p.Ctx, x, b.Recorder, s)
}

func ConnectbyTree(t *testing.T, p *Pair, b Backend, c *sql.Conn) {
	t.Helper()
	ConnectbyRun(t, p, b, c, Exec("CREATE TABLE cb_tree(k text,p text,pos integer)", 0))
	ConnectbyRun(t, p, b, c, Exec("INSERT INTO cb_tree VALUES ('row2','ignored',0),('row5','row2',1),('row9','row5',1),('row4','row2',2),('row6','row4',1),('row8','row6',1)", 6))
}

var ConnectbyTreeRows = [][]any{
	{"row2", nil, int64(0), "row2", int64(1)},
	{"row5", "row2", int64(1), "row2~row5", int64(2)},
	{"row9", "row5", int64(2), "row2~row5~row9", int64(3)},
	{"row4", "row2", int64(1), "row2~row4", int64(4)},
	{"row6", "row4", int64(2), "row2~row4~row6", int64(5)},
	{"row8", "row6", int64(3), "row2~row4~row6~row8", int64(6)},
}

var ConnectbyDefs = []string{
	"node text,parent text,level integer",
	"node text,parent text,level integer,branch text",
	"node text,parent text,level integer,serial integer",
	"node text,parent text,level integer,branch text,serial integer",
}

var ConnectbyArgs = []string{
	"'cb_tree','k','p',$1::text,$2::integer",
	"'cb_tree','k','p',$1::text,$2::integer,$3::text",
	"'cb_tree','k','p','pos',$1::text,$2::integer",
	"'cb_tree','k','p','pos',$1::text,$2::integer,$3::text",
}

func ConnectbyQuery(variant int, args string) string {
	q := "SELECT * FROM cb_part.connectby(" + args + ") AS t(" + ConnectbyDefs[variant] + ")"
	if variant >= 2 {
		q += " ORDER BY serial"
	}
	return q
}

func ConnectbySelect(variant int, q string, rows [][]any, args ...any) Step {
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
	s := Select(q, columns, types, rows, args...)
	s.Bag = variant < 2
	return s
}

func ConnectbyLayout(variant int, rows [][]any) [][]any {
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

var ConnectbySignatures = []string{"text,text,text,text,integer", "text,text,text,text,integer,text", "text,text,text,text,text,integer", "text,text,text,text,text,integer,text"}

const CrosstabSource = "SELECT k,category,v FROM ct_source ORDER BY seq"

func CrosstabSeedInSchema(t *testing.T, p *Pair, b Backend, c *sql.Conn, schema string) {
	t.Helper()
	install := "CREATE EXTENSION tablefunc"
	if schema != "" {
		install += " SCHEMA " + schema
	}
	for _, s := range []Step{
		Exec(install, 0),
		Exec("CREATE TABLE ct_source(seq integer,k text,category text,v text)", 0),
		Exec("INSERT INTO ct_source VALUES (1,'g1','z','A'),(2,'g1','a','C'),(3,'g2','x','B1'),(4,'g2','x','B2'),(5,'g2',NULL,'B3'),(6,'g2','q','B4'),(7,'g2','r','B5'),(8,'g3',NULL,NULL)", 8),
	} {
		RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func CrosstabQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func CrosstabWant(q string, args ...any) Step {
	return Select(q, []string{"k", "c1", "c2", "c3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}, args...)
}

func CrosstabHeld(t *testing.T, p *Pair, c *sql.Conn, q string) HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return HeldStatement{Executor: c, Statement: s, SQL: q}
}

func TablefuncNormalRows(n int, mean float64) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{mean}
	}
	return rows
}

func TablefuncNormalSelect(q string, n int, mean float64, args ...any) Step {
	s := Select(q, []string{"normal_rand"}, []string{"FLOAT8"}, TablefuncNormalRows(n, mean), args...)
	s.Bag = true
	return s
}
