package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestAlterStorageParser(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `IF EXISTS "é"`, "IF EXISTS t*", "IF EXISTS ONLY t", "IF EXISTS ONLY(t)"} {
		query := `ALTER TABLE ` + target + ` ALTER COLUMN "T é" SET STORAGE "eXtErNaL", ALTER t SET COMPRESSION PGLZ, ALTER t SET STORAGE DEFAULT`
		statements, err := Parse(query)
		if err != nil { t.Fatal(query, err) }
		raw := statements[0].(*AlterTable)
		if len(raw.Actions) != 3 || raw.Only != strings.Contains(target, "ONLY") || raw.IfExists != strings.HasPrefix(target, "IF") { t.Fatal(raw) }
		for i, expected := range []struct{kind AlterTableActionKind; value string}{{SetStorage,"eXtErNaL"},{SetCompression,"pglz"},{SetStorage,"default"}} {
			action := raw.Actions[i]
			if action.Action != expected.kind || action.Property.Text != expected.value { t.Fatal(action) }
			for _, pos := range []Position{action.Pos, action.Column.Pos, action.Property.Pos} {
				if pos.Character != utf8.RuneCountInString(query[:pos.Offset])+1 { t.Fatal("lost position", pos) }
			}
			if !strings.HasPrefix(query[action.Pos.Offset:], "ALTER") { t.Fatal(action.Pos) }
		}
		if raw.Actions[0].Column.Text != "T é" { t.Fatal(raw.Actions[0]) }
		if _, err := Parse(`ALTER TABLE other ALTER t SET STORAGE bogus`); err != nil { t.Fatal(err) }
		if raw.Actions[0].Property.Text != "eXtErNaL" { t.Fatal("later parse mutated AST") }
	}
	for _, expected := range []struct {
		clause string
		kind AlterTableActionKind
		value string
	}{
		{`SET STORAGE bogus`, SetStorage, "bogus"},
		{`SET COMPRESSION bogus`, SetCompression, "bogus"},
		{`SET COMPRESSION "PGLZ"`, SetCompression, "PGLZ"},
		{`SET COMPRESSION DEFAULT`, SetCompression, "default"},
	} {
		query := `ALTER TABLE IF EXISTS storage_absent ALTER t ` + expected.clause
		statements, err := Parse(query)
		if err != nil { t.Fatal(query, err) }
		raw := statements[0].(*AlterTable)
		if !raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != expected.kind || raw.Actions[0].Property.Text != expected.value {
			t.Fatal(query, raw)
		}
	}
	for _, query := range []string{
		`ALTER TABLE t ALTER t SET STORAGE`,
		`ALTER TABLE t ALTER t SET COMPRESSION`,
		`ALTER TABLE t ALTER t SET STORAGE 'plain'`,
		`ALTER TABLE t ALTER t SET COMPRESSION 'pglz'`,
		`ALTER TABLE t ALTER t SET STORAGE external garbage`,
		`ALTER TABLE t ALTER t SET COMPRESSION pglz,`,
		`ALTER TABLE t ALTER t SET STORAGE plain; INSERT INTO`,
	} {
		if statements, err := Parse(query); err == nil || statements != nil { t.Fatal("accepted malformed complete input", query, statements, err) }
	}
	for _, tc := range []struct{ query, token string }{
		{`ALTER TABLE storage_case ALTER t SET STORAGE 'plain'`, "'plain'"},
		{`ALTER TABLE storage_case ALTER t SET COMPRESSION 'pglz'`, "'pglz'"},
		{`ALTER TABLE "é" ALTER t SET STORAGE 'plain'`, "'plain'"},
		{`ALTER TABLE t ALTER t SET STORAGE`, ""},
		{`ALTER TABLE t ALTER t SET COMPRESSION`, ""},
		{`ALTER TABLE t ALTER t SET STORAGE external garbage`, "garbage"},
		{`ALTER TABLE t ALTER t SET COMPRESSION pglz garbage`, "garbage"},
		{`ALTER TABLE g ALTER COLUMN b SET EXPRESSION IF EXISTS AS(a+2)`, "IF"},
	} {
		statements, err := Parse(tc.query)
		offset := len(tc.query)
		message := "syntax error at end of input"
		if tc.token != "" {
			offset = strings.Index(tc.query, tc.token)
			message = `syntax error at or near "` + tc.token + `"`
		}
		want := sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:message, Position:strconv.Itoa(utf8.RuneCountInString(tc.query[:offset])+1)}
		var got *sqlstate.Error
		if statements != nil || !errors.As(err, &got) || *got != want { t.Fatal(tc.query, statements, err, got, want) }
	}
}
