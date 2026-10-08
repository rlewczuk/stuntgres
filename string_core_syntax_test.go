package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestStringCoreSyntaxAndLabels(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []stringCoreCase{
		{"SELECT ltrim(NULL) AS v", nil, "TEXT"},
		{"SELECT rtrim('x',NULL) AS v", nil, "TEXT"},
		{"SELECT trim(leading from ' x ') AS v", "x ", "TEXT"},
		{"SELECT trim(trailing from ' x ') AS v", " x", "TEXT"},
		{"SELECT btrim('a\t  '::char(8),'a') AS v", "\t ", "TEXT"},
		{"SELECT \"normalize\"('x','NFC '::char(5)) AS v", "x", "TEXT"},
		{"SELECT normalize('é',nFc) AS v", "é", "TEXT"},
		{"SELECT 'ⓑ' IS NFKC NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'b' IS NFKC NORMALIZED AS v", true, "BOOL"},
		{"SELECT 'é' IS NFKD NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NFKD NORMALIZED AS v", true, "BOOL"},
		{"SELECT is_normalized('é','nFd') AS v", true, "BOOL"},
		{"SELECT normalize('A̅́') AS v", "A̅́", "TEXT"},
		{"SELECT normalize('̀̕',NFD) AS v", "̀̕", "TEXT"},
		{"SELECT 'x'::text||setseed(NULL) AS v", nil, "TEXT"},
		{"SELECT setseed(NULL)||'x'::text AS v", nil, "TEXT"},
	} {
		stringCoreCheck(t, db, tc)
	}
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT trim(' x '),trim(leading from ' x '),trim(trailing from ' x ')", []string{"btrim", "ltrim", "rtrim"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"x", "x ", " x"}}},
		{"SELECT normalize('é'),'é' IS NORMALIZED,'é' IS NOT NORMALIZED,'a'||'b'", []string{"normalize", "is_normalized", "?column?", "?column?"}, []string{"TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"é", true, true, "ab"}}},
	} {
		rows, err := db.Query(tc.q)
		queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = s.Query()
		queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
		s.Close()
	}
}
