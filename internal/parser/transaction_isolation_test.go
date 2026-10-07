package parser

import "testing"

func TestTransactionIsolationGrammar(t *testing.T) {
	for _, prefix := range []string{"BEGIN", "BEGIN WORK", "BEGIN TRANSACTION", "START TRANSACTION", "SET TRANSACTION", "SET SESSION CHARACTERISTICS AS TRANSACTION"} {
		for _, modes := range []string{"ISOLATION LEVEL READ COMMITTED", "ISOLATION LEVEL READ UNCOMMITTED", "ISOLATION LEVEL REPEATABLE READ", "READ ONLY", "READ WRITE", "ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE", "READ ONLY, READ WRITE ISOLATION LEVEL REPEATABLE READ"} {
			statements, err := Parse(prefix + " " + modes)
			if err != nil || len(statements) != 1 {
				t.Fatalf("%s %s: %v %v", prefix, modes, statements, err)
			}
			controlParseError(t, prefix+" "+modes+" junk", "42601", "", "")
		}
	}
	statements, err := Parse("BEGIN READ ONLY, READ WRITE ISOLATION LEVEL READ UNCOMMITTED, ISOLATION LEVEL REPEATABLE READ")
	if err != nil {
		t.Fatal(err)
	}
	modes := statements[0].(*TransactionControl).Modes
	if len(modes) != 4 || !modes[0].ReadOnly || modes[1].ReadOnly || modes[2].Isolation != IsolationReadUncommitted || modes[3].Isolation != IsolationRepeatableRead {
		t.Fatal(modes)
	}
	for _, query := range []string{"SET TRANSACTION", "SET SESSION CHARACTERISTICS TRANSACTION READ ONLY", "SET SESSION CHARACTERISTICS AS TRANSACTION READ", "BEGIN READ WRITE,"} {
		controlParseError(t, query, "42601", "", "")
	}
}
