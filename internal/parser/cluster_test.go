package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestClusterGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql, table, index string
		parts             int
	}{
		{"CLUSTER", "", "", 0}, {"CLUSTER t", "t", "", 1}, {"CLUSTER s.t USING a", "t", "a", 2}, {`/*é*/ CLUSTER s."Té" USING "Ié"`, "Té", "Ié", 2},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		c, ok := statements[0].(*Cluster)
		if !ok || len(c.Table.Parts) != tc.parts || c.Index.Text != tc.index || tc.parts > 0 && c.Table.Parts[tc.parts-1].Text != tc.table {
			t.Fatal(tc.sql, c)
		}
		if tc.table == "Té" && (c.Pos.Character != 7 || c.Index.Pos.Character != 28) {
			t.Fatal("character source positions", c)
		}
	}
	for _, tc := range []struct{ sql, message, position string }{
		{"CLUSTER t USING", "syntax error at end of input", "16"},
		{"CLUSTER t USING public.a", `syntax error at or near "."`, "23"},
		{"CLUSTER t USING a junk", `syntax error at or near "junk"`, "19"},
	} {
		statements, err := Parse(tc.sql)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatal(tc.sql, statements, diagnostic, err)
		}
	}
	for _, sql := range []string{"CREATE TABLE cluster(cluster int)", "CREATE TABLE t(v cluster)", "SELECT cluster(cluster) FROM cluster", "SELECT cluster 'value'", "SELECT 1 AS cluster", "SET cluster=1", "SHOW cluster", "ALTER TABLE t CLUSTER ON a", "ALTER TABLE t SET WITHOUT CLUSTER"} {
		if _, err := Parse(sql); err != nil {
			t.Fatal(sql, err)
		}
	}
}
