package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCopyParserBinaryQueryFrom(t *testing.T) {
	statements, err := Parse("COPY (SELECT 1) FROM STDIN WITH(FORMAT binary)")
	var record *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"FROM\"", Position: "17"}
	if statements != nil || !errors.As(err, &record) || *record != want {
		t.Fatalf("statements=%#v diagnostic=%#v err=%v", statements, record, err)
	}
}
