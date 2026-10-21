package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestListPartitionGrammar(t *testing.T) {
	query := `CREATE TABLE "é"(k INT) PARTITION BY LIST(k)`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	root := statements[0].(*CreateTable)
	if root.PartitionBy.Strategy != PartitionList || len(root.PartitionBy.Keys) != 1 || root.PartitionBy.Keys[0].Column.Text != "k" {
		t.Fatal(root.PartitionBy)
	}
	query = `CREATE TABLE "é" PARTITION OF root FOR VALUES IN(1+0,NULL,MINVALUE,MAXVALUE,'MINVALUE',2,3,4) PARTITION BY LIST((lower(payload)))`
	statements, err = Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	child := statements[0].(*CreateTable)
	bound := child.PartitionOf.Bound
	if bound.Strategy != PartitionList || len(bound.Values) != 8 || len(bound.Lower) != 0 || len(bound.Upper) != 0 || child.PartitionBy.Strategy != PartitionList {
		t.Fatal(child)
	}
	for i, token := range []string{"1+0", "NULL", "MINVALUE", "MAXVALUE", "'MINVALUE'", "2,3", "3,4", "4)"} {
		datum := bound.Values[i]
		want := utf8.RuneCountInString(query[:strings.Index(query, token)]) + 1
		if datum.Kind != 0 || datum.Expression == nil || datum.Pos.Character != want {
			t.Fatal(i, datum, want)
		}
	}
	for _, i := range []int{2, 3} {
		if _, ok := bound.Values[i].Expression.(ColumnExpr); !ok {
			t.Fatal("LIST min/max identifier became a range sentinel", bound.Values[i])
		}
	}
	for _, valid := range []string{
		"CREATE TABLE c PARTITION OF p DEFAULT PARTITION BY LIST(k)",
		"CREATE TABLE c PARTITION OF p FOR VALUES FROM(MINVALUE) TO(MAXVALUE)",
		"CREATE TABLE p(k INT) PARTITION BY HASH(k)",
		"CREATE TABLE list(k INT)",
		"SELECT sum(k) OVER(PARTITION BY k ORDER BY k) FROM t",
	} {
		if _, err := Parse(valid); err != nil {
			t.Fatal(valid, err)
		}
	}
	for _, invalid := range []struct{ query, token string }{
		{"CREATE TABLE c PARTITION OF p FOR VALUES IN()", ")"},
		{"CREATE TABLE c PARTITION OF p FOR VALUES IN(1,)", ")"},
		{`/*é*/ CREATE TABLE p(k INT) PARTITION BY LIST(k) trailing`, "trailing"},
	} {
		_, err := Parse(invalid.query)
		var diagnostic *sqlstate.Error
		want := strconv.Itoa(utf8.RuneCountInString(invalid.query[:strings.Index(invalid.query, invalid.token)]) + 1)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != want {
			t.Fatal(invalid, diagnostic, err, want)
		}
	}
}
