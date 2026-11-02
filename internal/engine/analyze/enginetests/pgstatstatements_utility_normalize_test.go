package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintUtilityNormalization(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"SAVEPOINT \"snow 雪\"", "SAVEPOINT $1"},
		{"RELEASE SAVEPOINT first_name", "RELEASE SAVEPOINT $1"},
		{"ROLLBACK WORK TO SAVEPOINT first_name", "ROLLBACK WORK TO SAVEPOINT $1"},
		{"PREPARE TRANSACTION 'snow 雪'", "PREPARE TRANSACTION $1"},
		{"COMMIT PREPARED 'first'", "COMMIT PREPARED $1"},
		{"ROLLBACK PREPARED 'first'", "ROLLBACK PREPARED $1"},
		{"DEALLOCATE PREPARE first_name", "DEALLOCATE PREPARE $1"},
		{"SET application_name='first'", "SET application_name=$1"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			jumble, anchor := analyze.JumbleUtility(enginetest.Statement(t, tc.source))
			got, _ := parser.NormalizeStatementText(tc.source, anchor, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if got != tc.want {
				t.Fatalf("normalized %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityForeignOptionOrder(t *testing.T) {
	source := "CREATE FOREIGN TABLE pgss_foreign(a integer OPTIONS (one 'x',two 'y'),b integer OPTIONS (three 'z')) SERVER pgss_server"
	statement := enginetest.Statement(t, source).(*parser.ForeignDDL)
	first, _ := analyze.JumbleUtility(statement)
	// Native map insertion order is not parser order: the actual retained column
	// declarations own the released list, while option order within a column stays
	// selected. No map iteration/sorting can replace those declaration positions.
	original := statement.ColumnOptions
	statement.ColumnOptions = map[string][]parser.ForeignOption{"b": original["b"], "a": original["a"]}
	reordered, _ := analyze.JumbleUtility(statement)
	if first.QueryID() != reordered.QueryID() {
		t.Fatal("map insertion order replaced declaration order")
	}
	options := statement.ColumnOptions["a"]
	options[0], options[1] = options[1], options[0]
	changed, _ := analyze.JumbleUtility(statement)
	if first.QueryID() == changed.QueryID() {
		t.Fatal("selected per-column option list order was discarded")
	}
}

func TestPgStatStatementsFingerprintUtilityMissingForeignOrder(t *testing.T) {
	defer func() {
		got, ok := recover().(string)
		if !ok || got != "stuntgres: foreign column options lack declaration order" {
			t.Fatalf("unexpected missing order signal: %q", got)
		}
	}()
	analyze.JumbleUtility(&parser.ForeignDDL{ColumnOptions: map[string][]parser.ForeignOption{"a": {{}}}})
}
