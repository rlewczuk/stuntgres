package parser

import "testing"

func TestSavepointsGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		kind SavepointControlKind
	}{
		{"SAVEPOINT s", CreateSavepoint}, {"RELEASE s", ReleaseSavepoint}, {"RELEASE SAVEPOINT s", ReleaseSavepoint},
		{"ROLLBACK TO s", RollbackSavepoint}, {"ROLLBACK WORK TO SAVEPOINT s", RollbackSavepoint}, {"ROLLBACK TRANSACTION TO s", RollbackSavepoint},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, err)
		}
		control := statements[0].(*SavepointControl)
		if control.Kind != tc.kind || control.Name.Text != "s" || control.Pos.Character != 1 {
			t.Fatal(control)
		}
	}
	for _, q := range []string{"ABORT TO s", "SAVEPOINT", "RELEASE", "ROLLBACK TO", "ROLLBACK TO s AND CHAIN", "SAVEPOINT s junk", "RELEASE s junk"} {
		controlParseError(t, q, "42601", "", "")
	}
	statements, err := Parse("; /* head */ sAvEpOiNt s; ROLLBACK TO s; RELEASE s; -- tail\n")
	if err != nil || len(statements) != 3 || statements[0].(*SavepointControl).Pos.Character != 14 {
		t.Fatal(statements, err)
	}
}

func TestSavepointsNames(t *testing.T) {
	for _, tc := range []struct{ sql, name string }{{"SAVEPOINT MiXeD", "mixed"}, {`SAVEPOINT "MiXeD"`, "MiXeD"}, {`SAVEPOINT "a""b"`, `a"b`}, {"SAVEPOINT savepoint", "savepoint"}, {"RELEASE SAVEPOINT", "savepoint"}, {"RELEASE SAVEPOINT savepoint", "savepoint"}, {"ROLLBACK TO SAVEPOINT", "savepoint"}, {"ROLLBACK TO SAVEPOINT savepoint", "savepoint"}} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, err)
		}
		if got := statements[0].(*SavepointControl).Name.Text; got != tc.name {
			t.Fatal(got, tc.name)
		}
	}
}
