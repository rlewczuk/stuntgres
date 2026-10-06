package parser

import (
	"testing"
)

func TestDefaultSyntaxAndRequestSeparation(t *testing.T) {
	statements, err := Parse(`CREATE TABLE d(i INT NULL DEFAULT 7 CONSTRAINT nn NOT NULL, b BOOL CONSTRAINT db DEFAULT (1 < 2 AND NOT FALSE), n INT DEFAULT NULL DEFAULT 2)`)
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*CreateTable)
	c := s.Elements[0].Column.Constraints
	if len(c) != 3 || c[0].Default != nil || c[1].Default == nil || !c[2].NotNull || c[2].Name.Text != "nn" {
		t.Fatal(c)
	}
	if s.Elements[1].Column.Constraints[0].Name.Text != "db" {
		t.Fatal(s)
	}
	if len(s.Elements[2].Column.Constraints) != 2 {
		t.Fatal("duplicate defaults must reach utility Execute")
	}
	statements, err = Parse(`INSERT INTO d(i,b) VALUES(DEFAULT,NULL),(1,DEFAULT); INSERT INTO d DEFAULT VALUES`)
	if err != nil {
		t.Fatal(err)
	}
	first := statements[0].(*Insert)
	if _, ok := first.Rows[0][0].(DefaultExpr); !ok {
		t.Fatal(first)
	}
	if _, ok := first.Rows[0][1].(LiteralExpr); !ok {
		t.Fatal("NULL was converted to DEFAULT")
	}
	if !statements[1].(*Insert).DefaultValues || len(statements[1].(*Insert).Rows) != 1 {
		t.Fatal(statements[1])
	}
	for _, q := range []string{`SELECT DEFAULT FROM d`, `SELECT i FROM d WHERE DEFAULT`, `CREATE TABLE x(i INT DEFAULT DEFAULT)`, `INSERT INTO d VALUES(1 < 2)`, `SELECT current_setting(DEFAULT)`, `INSERT INTO d VALUES()`, `CREATE TABLE x(i INT DEFAULT 7); SELECT * FROM`} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("accepted unsupported/incomplete syntax", q, statements, err)
		}
	}
}

func TestDefaultOracleSyntaxBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, query, message, position string }{
		{"targets", `INSERT INTO all_defaults(i) DEFAULT VALUES`, `syntax error at or near "DEFAULT"`, "29"},
		{"not", `CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`, `syntax error at or near "NOT"`, "125"},
		{"and", `CREATE TABLE bad(b BOOL DEFAULT TRUE AND FALSE)`, `syntax error at or near "AND"`, "38"},
		{"or", `CREATE TABLE bad(b BOOL DEFAULT TRUE OR FALSE)`, `syntax error at or near "OR"`, "38"},
	} {
		t.Run(tc.name, func(t *testing.T) { keywordDiagnostic(t, tc.query, "42601", tc.message, tc.position) })
	}
}
