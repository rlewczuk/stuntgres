package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConnectbyTreeAndSignatures(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		connectbyTree(t, p, b, c)
		for v := range connectbyDefs {
			q := connectbyQuery(v, connectbyArgs[v])
			args := []any{"row2", 0}
			if v == 1 || v == 3 {
				args = append(args, "~")
			}
			s := connectbySelect(v, q, connectbyLayout(v, connectbyTreeRows), args...)
			connectbyRun(t, p, b, c, s)
			connectbyRun(t, p, b, c, fixture.Prepared(s))
			held := connectbyHold(t, p, c, q)
			connectbyRun(t, p, b, held, s)
			args[0] = "row4"
			args[1] = 1
			rows := [][]any{{"row4", nil, int64(0), "row4", int64(1)}, {"row6", "row4", int64(1), "row4~row6", int64(2)}}
			if v == 1 || v == 3 {
				args[2] = "::"
				rows[1][3] = "row4::row6"
			}
			connectbyRun(t, p, b, held, connectbySelect(v, q, connectbyLayout(v, rows), args...))
		}
		connectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_int(k integer,p integer,pos integer)", 0))
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_int VALUES(-2,NULL,9),(3,-2,5),(4,3,7)", 3))
		q := "SELECT * FROM cb_part.connectby('cb_int','k','p','pos','-2',0,'/') AS t(node integer,parent integer,level integer,branch text,serial integer) ORDER BY serial"
		connectbyRun(t, p, b, c, fixture.Select(q, []string{"node", "parent", "level", "branch", "serial"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(-2), nil, int64(0), "-2", int64(1)}, {int64(3), int64(-2), int64(1), "-2/3", int64(2)}, {int64(4), int64(3), int64(2), "-2/3/4", int64(3)}}))
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES('tie1','peers',NULL),('tie2','peers',NULL)", 2))
		q = connectbyQuery(2, connectbyArgs[2])
		// Peer order is deliberately not asserted: remove the serial projection.
		q = "SELECT node,parent,level FROM (" + q + ") AS peers"
		s := fixture.Select(q, []string{"node", "parent", "level"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"peers", nil, int64(0)}, {"tie1", "peers", int64(1)}, {"tie2", "peers", int64(1)}}, "peers", 1)
		s.Bag = true
		connectbyRun(t, p, b, c, s)
	})
}

func TestReferenceConnectbyDepthAndNulls(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		connectbyTree(t, p, b, c)
		q := connectbyQuery(3, connectbyArgs[3])
		held := connectbyHold(t, p, c, q)
		for _, depth := range []int{0, -1, -2147483648, 1, 2} {
			rows := connectbyTreeRows
			if depth == 1 {
				rows = [][]any{{"row2", nil, int64(0), "row2", int64(1)}, {"row5", "row2", int64(1), "row2~row5", int64(2)}, {"row4", "row2", int64(1), "row2~row4", int64(3)}}
			}
			if depth == 2 {
				rows = connectbyTreeRows[:5]
			}
			s := connectbySelect(3, q, rows, "row2", depth, "~")
			connectbyRun(t, p, b, c, s)
			connectbyRun(t, p, b, held, s)
		}
		connectbyRun(t, p, b, c, connectbySelect(3, q, [][]any{{"absent", nil, int64(0), "absent", int64(1)}}, "absent", 0, "~"))
		for v := range connectbyDefs {
			literals := []string{"'cb_missing'::text", "'k'::text", "'p'::text", "'row2'::text", "0::integer"}
			if v >= 2 {
				literals = append(literals[:3], "'pos'::text", "'row2'::text", "0::integer")
			}
			if v == 1 || v == 3 {
				literals = append(literals, "'~'::text")
			}
			for i := range literals {
				copyArgs := append([]string{}, literals...)
				typ := "text"
				if i == len(literals)-1 && (v == 0 || v == 2) || i == len(literals)-2 && (v == 1 || v == 3) {
					typ = "integer"
				}
				copyArgs[i] = "NULL::" + typ
				s := connectbySelect(v, connectbyQuery(v, strings.Join(copyArgs, ",")), nil)
				connectbyRun(t, p, b, c, s)
				connectbyRun(t, p, b, c, fixture.Prepared(s))
			}
		}
		q = "SELECT * FROM cb_part.connectby(NULL::text,'k','p','root',0) AS t(node text,parent text,level text)"
		connectbyRun(t, p, b, c, fixture.Select(q, []string{"node", "parent", "level"}, []string{"TEXT", "TEXT", "TEXT"}, nil))
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES(NULL,'dup_root',1),('orphan',NULL,2),('dup_root','dup_root',3),('dup','dup_root',4),('dup','dup_root',5)", 5))
		connectbyRun(t, p, b, c, connectbySelect(0, connectbyQuery(0, connectbyArgs[0]), [][]any{{"dup_root", nil, int64(0)}, {"dup", "dup_root", int64(1)}, {"dup", "dup_root", int64(1)}}, "dup_root", 0))
		connectbyRun(t, p, b, c, connectbySelect(0, connectbyQuery(0, connectbyArgs[0]), [][]any{{"", nil, int64(0)}}, "", 0))
		for _, root := range []string{"雪", "-7"} {
			connectbyRun(t, p, b, c, connectbySelect(0, connectbyQuery(0, connectbyArgs[0]), [][]any{{root, nil, int64(0)}}, root, 0))
		}
	})
}

func TestReferenceConnectbyCyclesAndSeparators(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		connectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_tree(k text,p text,pos integer)", 0))
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES('b','a',1),('c','b',1),('a','c',1),('self','self',1),('b','a~b',1),('b','ab',1),('111','11',1),('1','111',1),('10','11',2)", 9))
		q := connectbyQuery(1, connectbyArgs[1])
		bounded := connectbySelect(1, q, [][]any{{"a", nil, int64(0), "a"}, {"b", "a", int64(1), "a~b"}, {"c", "b", int64(2), "a~b~c"}}, "a", 2, "~")
		connectbyRun(t, p, b, c, bounded)
		for _, depth := range []int{3, 0, -1} {
			s := connectbyError(q, "42P19", "infinite recursion detected", "")
			s.Args = []any{"a", depth, "~"}
			connectbyRun(t, p, b, c, s)
			connectbyRun(t, p, b, c, fixture.Prepared(s))
			connectbyRun(t, p, b, c, bounded)
		}
		connectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_atomic(v integer)", 0))
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_atomic VALUES(99)", 1))
		atomic := connectbyError("INSERT INTO cb_atomic SELECT level FROM cb_part.connectby('cb_tree','k','p','a',0) AS t(node text,parent text,level integer)", "42P19", "infinite recursion detected", "")
		atomic.SelectRows = false
		connectbyRun(t, p, b, c, atomic)
		connectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		connectbyRun(t, p, b, c, fixture.Prepared(atomic))
		connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_cycle AS SELECT * FROM cb_part.connectby('cb_tree','k','p','a',0) AS t(node text,parent text,level integer)", 0))
		connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_cycle_nested AS SELECT * FROM cb_cycle", 0))
		throughView := connectbyError("INSERT INTO cb_atomic SELECT level FROM cb_cycle_nested", "42P19", "infinite recursion detected", "")
		throughView.SelectRows = false
		connectbyRun(t, p, b, c, throughView)
		connectbyRun(t, p, b, c, fixture.Prepared(throughView))
		connectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		connectbyRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO cb_atomic SELECT level FROM cb_part.connectby('cb_tree','k','p','a',2) AS t(node text,parent text,level integer)", 3)))
		connectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(99)}}))
		for _, test := range []struct{ root, delimiter string }{{"a~b", "~"}, {"ab", ""}} {
			s := connectbyError(q, "42P19", "infinite recursion detected", "")
			s.Args = []any{test.root, 0, test.delimiter}
			connectbyRun(t, p, b, c, s)
		}
		s := connectbyError(connectbyQuery(0, connectbyArgs[0]), "42P19", "infinite recursion detected", "")
		s.Args = []any{"a~b", 0}
		connectbyRun(t, p, b, c, s)
		for _, test := range []struct {
			root, delim string
			rows        [][]any
		}{
			{"a~b", "::", [][]any{{"a~b", nil, int64(0), "a~b"}, {"b", "a~b", int64(1), "a~b::b"}, {"c", "b", int64(2), "a~b::b::c"}, {"a", "c", int64(3), "a~b::b::c::a"}}},
			{"11", "-", [][]any{{"11", nil, int64(0), "11"}, {"111", "11", int64(1), "11-111"}, {"1", "111", int64(2), "11-111-1"}, {"10", "11", int64(1), "11-10"}}},
			{"self", "", [][]any{{"self", nil, int64(0), "self"}}},
		} {
			depth := 0
			if test.root == "a~b" {
				depth = 3
			}
			connectbyRun(t, p, b, c, connectbySelect(1, q, test.rows, test.root, depth, test.delim))
		}
		connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES('left','shared',1),('right','shared',2),('leaf','left',1),('leaf','right',1)", 4))
		connectbyRun(t, p, b, c, connectbySelect(1, q, [][]any{{"shared", nil, int64(0), "shared"}, {"left", "shared", int64(1), "shared::left"}, {"leaf", "left", int64(2), "shared::left::leaf"}, {"right", "shared", int64(1), "shared::right"}, {"leaf", "right", int64(2), "shared::right::leaf"}}, "shared", 0, "::"))
	})
}
