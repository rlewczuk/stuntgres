package parser

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestTransactionControlsGrammar(t *testing.T) {
	starts := []string{"BEGIN", "BEGIN WORK", "BEGIN TRANSACTION", "START TRANSACTION"}
	for _, q := range starts {
		controlParse(t, q, KindStart, false)
	}
	for _, word := range []string{"COMMIT", "END", "ROLLBACK", "ABORT"} {
		kind := KindCommit
		if word == "ROLLBACK" || word == "ABORT" {
			kind = KindRollback
		}
		for _, optional := range []string{"", " WORK", " TRANSACTION"} {
			for _, suffix := range []string{"", " AND CHAIN", " AND NO CHAIN"} {
				controlParse(t, word+optional+suffix, kind, suffix == " AND CHAIN")
				controlParseError(t, word+optional+suffix+" junk", "42601", "", "")
			}
		}
	}
	for _, tc := range []struct {
		q    string
		kind TransactionControlKind
	}{
		{"PREPARE TRANSACTION 'x''y'", KindPrepareTransaction},
		{"COMMIT PREPARED 'x''y'", KindCommitPrepared},
		{"ROLLBACK PREPARED 'x''y'", KindRollbackPrepared},
	} {
		controlParse(t, tc.q, tc.kind, false)
		statements, err := Parse(tc.q)
		if err != nil || statements[0].(*TransactionControl).GID != "x'y" {
			t.Fatal(statements, err)
		}
		controlParseError(t, tc.q+" junk", "42601", "", "")
	}
	for _, q := range []string{"START", "START WORK", "START TRANSACTION TRANSACTION", "BEGIN READ", "BEGIN ISOLATION LEVEL", "BEGIN ISOLATION LEVEL READ", "BEGIN ISOLATION LEVEL REPEATABLE", "BEGIN NOT", "BEGIN READ WRITE,", "SAVEPOINT", "RELEASE", "ROLLBACK TO", "PREPARE TRANSACTION", "COMMIT PREPARED", "ROLLBACK PREPARED"} {
		controlParseError(t, q, "42601", "", "")
	}
	controlParseError(t, "COMMIT AND NO", "42601", "syntax error at end of input", "14")
	controlParseError(t, "BEGIN AND CHAIN", "42601", "syntax error at or near \"AND\"", "7")
	controlParseError(t, "ROLLBACK WORK TRANSACTION", "42601", "syntax error at or near \"TRANSACTION\"", "15")
	controlParse(t, " /* hi */ bEgIn work; -- tail\n", KindStart, false)
	stmts, err := Parse(";BEGIN; INSERT INTO t VALUES(1); END TRANSACTION AND CHAIN;; ABORT;")
	if err != nil || len(stmts) != 4 {
		t.Fatal(stmts, err)
	}
	controlParseError(t, `"BEGIN"`, "42601", "", "")
}

func controlParse(t *testing.T, q string, kind TransactionControlKind, chain bool) {
	t.Helper()
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(q, statements, err)
	}
	c, ok := statements[0].(*TransactionControl)
	if !ok || c.Kind != kind || c.Chain != chain {
		t.Fatal(q, statements)
	}
	first := strings.Index(strings.ToLower(q), "begin")
	if kind != KindStart || first < 0 {
		first = strings.IndexFunc(q, func(r rune) bool { return r != ' ' && r != ';' })
	}
	if c.Pos.Offset != first || c.Pos.Character != first+1 {
		t.Fatal(q, c.Pos, first)
	}
}
func controlParseError(t *testing.T, q, code, message, position string) {
	t.Helper()
	statements, err := Parse(q)
	var diagnostic *sqlstate.Error
	if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatal(q, statements, err)
	}
	if message != "" && diagnostic.Message != message {
		t.Fatal(q, diagnostic)
	}
	if position != "" && diagnostic.Position != position {
		t.Fatal(q, diagnostic)
	}
}
func TestTransactionControlsKeywordEligibility(t *testing.T) {
	for _, word := range []string{"begin", "start", "commit", "rollback", "abort", "work", "transaction", "no", "chain", "isolation", "level", "read", "savepoint", "release", "prepare", "prepared", "committed", "uncommitted", "repeatable", "serializable"} {
		for _, q := range []string{"CREATE TABLE " + word + "(" + word + " INT)", "INSERT INTO " + word + "(" + word + ") VALUES(1)", "SELECT " + word + " FROM " + word, "CREATE ROLE " + word, "SET " + word + " TO 'x'"} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		orderLabel(t, "SELECT v AS "+word+" FROM t", word)
		orderLabel(t, "SELECT v "+word+" FROM t", word)
	}
	for _, word := range []string{"end", "and", "only", "deferrable"} {
		controlParseError(t, "CREATE TABLE "+word+"(v INT)", "42601", "", "")
		orderLabel(t, "SELECT v AS "+word+" FROM t", word)
		if _, err := Parse("CREATE TABLE \"" + word + "\"(v INT)"); err != nil {
			t.Fatal(word, err)
		}
	}
}
