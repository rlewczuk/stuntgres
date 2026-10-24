package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLQueryLoopParser(t *testing.T) {
	for _, query := range []string{
		`SELECT /*é中*/ $1 AS i, 'loop' AS t ORDER BY i`,
		`SELECT $value$loop$value$ AS "loop", 'loop' AS t`,
		`WITH c AS (SELECT $1 AS i) SELECT i FROM c`,
		`INSERT INTO q VALUES ($1) RETURNING i`,
		`UPDATE q SET i=$1 RETURNING i`,
		`DELETE FROM q WHERE i=$1 RETURNING i`,
		`MERGE INTO q USING s ON q.i=s.i WHEN MATCHED THEN DELETE RETURNING q.i`,
		`WITH c AS (INSERT INTO q VALUES ($1) RETURNING i) SELECT i FROM c`,
	} {
		t.Run(strings.Fields(query)[0]+query, func(t *testing.T) {
			source := `<<外>> DECLARE r record; BEGIN <<row_scan>> FOR 外.r IN /*before*/ ` + query + ` /*after*/ LOOP CONTINUE row_scan WHEN false; EXIT row_scan; END LOOP row_scan; RETURN 7; END 外`
			raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			loop := raw.Statements[0].QueryLoop
			if loop == nil || loop.Body == nil || !loop.Body.IsLoop || loop.Body.Label.Text != "row_scan" || loop.Body.EndLabel.Text != "row_scan" {
				t.Fatal("lost query/body ownership")
			}
			if got := PLExpressionSource(source, loop.Start, loop.End); got != query {
				t.Fatalf("source boundary %q, want %q", got, query)
			}
			if source[loop.Delimiter.Offset:loop.Delimiter.Offset+4] != "LOOP" || loop.Start.Character != utf8.RuneCountInString(source[:loop.Start.Offset])+1 || loop.End.Character != utf8.RuneCountInString(source[:loop.End.Offset])+1 {
				t.Fatal("lost positioned delimiter")
			}
			before := *loop
			// A separate parser checkout must not overwrite a goyacc semantic cell.
			next, err := ParsePLpgSQL(`DECLARE z record; BEGIN FOR z IN SELECT 2 AS n LOOP END LOOP; RETURN 1; END`, PLReturnPolicy{})
			if err != nil || next.Statements[0].QueryLoop == loop || !reflect.DeepEqual(before, *loop) {
				t.Fatal("parser checkout aliased retained syntax", err)
			}
		})
	}
	for _, source := range []string{
		`DECLARE r record; BEGIN RETURN 7; FOR r IN SELECT 1 + LOOP END LOOP; END`,
		`DECLARE r record; BEGIN FOR r IN SELECT 1 LOOP END LOOP wrong; RETURN 7; END`,
		`DECLARE r record; BEGIN FOR r IN SELECT 1 LOOP END LOOP; RETURN 7; END junk`,
		`DECLARE r record; BEGIN FOR r IN SELECT (1 LOOP END LOOP; END`,
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed complete input", source)
		}
	}
}
