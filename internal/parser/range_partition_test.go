package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestRangePartitionAST(t *testing.T) {
	for _, query := range []string{
		`CREATE TABLE "schema"."root"(k INT,payload TEXT) PARTITION BY RANGE(k,((k+1)))`,
		`CREATE TABLE child PARTITION OF "schema"."root"(payload DEFAULT 'local',CONSTRAINT positive CHECK(k>0)) FOR VALUES FROM(1,MINVALUE) TO(3,MAXVALUE) PARTITION BY RANGE((k+1))`,
		`CREATE TABLE child PARTITION OF parent DEFAULT`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		table, ok := statements[0].(*CreateTable)
		if !ok || table.Pos.Character != 1 {
			t.Fatal(statements[0])
		}
		if table.PartitionOf != nil && table.PartitionOf.Pos.Character != utf8.RuneCountInString(query[:strings.Index(query, " PARTITION OF")])+2 {
			t.Fatal("lost PARTITION OF source token", table.PartitionOf.Pos)
		}
		if table.PartitionOf == nil {
			if table.PartitionBy == nil || len(table.PartitionBy.Keys) != 2 || table.PartitionBy.Keys[0].Column.Text != "k" || table.PartitionBy.Keys[1].Expression == nil {
				t.Fatal(table.PartitionBy)
			}
		} else if table.PartitionOf.Bound.Default {
			if table.PartitionBy != nil || len(table.PartitionOf.Bound.Lower) != 0 || len(table.PartitionOf.Bound.Upper) != 0 {
				t.Fatal(table.PartitionOf)
			}
		} else {
			bound := table.PartitionOf.Bound
			if len(bound.Lower) != 2 || len(bound.Upper) != 2 || bound.Lower[0].Expression == nil || bound.Upper[0].Expression == nil || bound.Lower[1].Kind != -1 || bound.Upper[1].Kind != 1 || table.PartitionBy == nil || len(table.Elements) != 2 {
				t.Fatal(table)
			}
			for _, side := range [2][]PartitionDatum{bound.Lower, bound.Upper} {
				for _, datum := range side {
					if datum.Pos.Character <= 0 {
						t.Fatal(datum)
					}
				}
			}
		}
	}
}

func TestRangePartitionContextAndFullInput(t *testing.T) {
	for _, query := range []string{
		`SELECT sum(k) OVER(PARTITION BY k ORDER BY k) FROM t`,
		`CREATE TABLE p(k INT) PARTITION BY RANGE((abs(k)))`,
		`CREATE TABLE p(k INT) PARTITION BY RANGE((k::BIGINT))`,
		`CREATE TABLE c PARTITION OF p FOR VALUES FROM(nextval('seq')) TO(20)`,
		`CREATE TABLE c PARTITION OF p FOR VALUES FROM(NULL) TO(3)`,
		`CREATE TABLE p(k INT)`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	query := `/*é*/ CREATE TABLE p(k INT) PARTITION BY RANGE(k) trailing`
	_, err := Parse(query)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "trailing"` {
		t.Fatal(err)
	}
	want := utf8.RuneCountInString(query[:strings.Index(query, "trailing")]) + 1
	if diagnostic.Position != strconv.Itoa(want) {
		t.Fatal(diagnostic.Position, want)
	}
}
