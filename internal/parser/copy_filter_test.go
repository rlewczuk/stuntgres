package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCopyFilterParser(t *testing.T) {
	q := "COPY café FROM STDIN WITH(ON_ERROR 'IgNoRe',REJECT_LIMIT -2,LOG_VERBOSITY verbose) WHERE TRUE"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	copy := statements[0].(*Copy)
	if copy.Where == nil || len(copy.Options) != 3 || copy.Options[0].Value != "IgNoRe" || !copy.Options[1].Numeric || copy.Options[1].Value != "-2" {
		t.Fatalf("lost COPY syntax: %+v", copy)
	}
	if copy.Options[2].Pos.Character != len([]rune(q[:strings.Index(q, "LOG_VERBOSITY")]))+1 {
		t.Fatal("byte-based option position")
	}
	for _, suffix := range []string{" trailing", "; SELECT ("} {
		if _, err := Parse(q + suffix); err == nil {
			t.Fatal("accepted incomplete suffix", suffix)
		}
	}
	for _, tc := range []struct {
		argument string
		kind     CopyArgument
		value    string
		columns  []string
	}{
		{"default", CopyArgumentScalar, "default", nil},
		{"(silent,verbose)", CopyArgumentColumns, "", []string{"silent", "verbose"}},
		{"('silent','verbose')", CopyArgumentColumns, "", []string{"silent", "verbose"}},
	} {
		statements, err := Parse("COPY t FROM STDIN WITH(LOG_VERBOSITY " + tc.argument + ")")
		if err != nil {
			t.Fatal(tc.argument, err)
		}
		option := statements[0].(*Copy).Options[0]
		if option.Argument != tc.kind || option.Value != tc.value || !option.HasValue || len(option.Columns) != len(tc.columns) {
			t.Fatalf("option %s lost shape: %+v", tc.argument, option)
		}
		for i, column := range tc.columns {
			if option.Columns[i].Text != column {
				t.Fatalf("option %s column[%d]=%q want=%q", tc.argument, i, option.Columns[i].Text, column)
			}
		}
	}
	q = "COPY café TO STDOUT WHERE TRUE"
	_, err = Parse(q)
	var diagnostic *sqlstate.Error
	wantPosition := strconv.Itoa(len([]rune(q[:strings.Index(q, "WHERE")])) + 1)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "WHERE clause not allowed with COPY TO" || diagnostic.Position != wantPosition {
		t.Fatal(err)
	}
}
