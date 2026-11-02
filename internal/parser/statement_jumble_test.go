package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPgStatStatementsTransactionSyntax(t *testing.T) {
	for _, tc := range []struct {
		source, gid string
		start       bool
	}{
		{"BEGIN", "", false},
		{"BEGIN WORK READ ONLY", "", false},
		{"START TRANSACTION", "", true},
		{"START TRANSACTION READ ONLY", "", true},
		{"/* 雪 */ PREPARE TRANSACTION 'snow 雪'", "'snow 雪'", false},
		{"COMMIT PREPARED 'first'", "'first'", false},
		{"ROLLBACK PREPARED 'first'", "'first'", false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			statements, err := Parse(tc.source)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			statement, ok := statements[0].(*TransactionControl)
			if !ok {
				t.Fatalf("unexpected statement %T", statements[0])
			}
			if statement.StartKeyword != tc.start {
				t.Fatalf("START provenance %v, want %v", statement.StartKeyword, tc.start)
			}
			if tc.gid != "" {
				at := strings.Index(tc.source, tc.gid)
				want := Position{Offset: at, Character: utf8.RuneCountInString(tc.source[:at]) + 1}
				if statement.GIDPosition != want {
					t.Fatalf("GID position %+v, want %+v", statement.GIDPosition, want)
				}
			} else if statement.GIDPosition != (Position{}) {
				t.Fatalf("spurious GID position %+v", statement.GIDPosition)
			}
		})
	}
}

func TestPgStatStatementsRoleSyntax(t *testing.T) {
	for _, kind := range []string{"ROLE", "USER", "GROUP"} {
		t.Run(kind, func(t *testing.T) {
			statements, err := Parse("CREATE " + kind + " role_kind_owner")
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			role, ok := statements[0].(*CreateRole)
			if !ok || role.Kind != strings.ToLower(kind) || role.DefaultLogin != (kind == "USER") {
				t.Fatal("lost introducer or changed runtime default", statements[0])
			}
		})
	}
}
