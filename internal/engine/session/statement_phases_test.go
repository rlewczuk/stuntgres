package session

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func sqlText(err error) string {
	if err == nil {
		return ""
	}
	e := err.(*sqlstate.Error)
	return e.Code + " " + e.Message
}

func TestReadOnlyRefusal(t *testing.T) {
	if err := ReadOnlyRefusal(&parser.CreateTable{}, true); sqlText(err) != "25006 cannot execute CREATE TABLE in a read-only transaction" {
		t.Fatal(err)
	}
	if err := ReadOnlyRefusal(&parser.CreateTable{}, false); err != nil {
		t.Fatal(err)
	}
	if err := ReadOnlyRefusal(&parser.Insert{}, true); err != nil || !ReadOnlyExempt(&parser.Insert{}) {
		t.Fatal("DML checks its own targets", err)
	}
	if err := ReadOnlyRefusal(&parser.Listen{}, true); err != nil {
		t.Fatal("non-modifying utility", err)
	}
}

func TestExplicitUtilityRefusal(t *testing.T) {
	concurrent := parser.Position{Character: 1}
	for _, tc := range []struct {
		stmt    parser.Statement
		state   TxState
		inBlock bool
		want    string
	}{
		{&parser.CreateDatabaseStmt{}, TxActive, true, "25001 CREATE DATABASE cannot run inside a transaction block"},
		{&parser.CreateDatabaseStmt{}, TxImplicit, false, ""},
		{&parser.CreateTablespace{}, TxImplicit, true, "25001 CREATE TABLESPACE cannot run inside a transaction block"},
		{&parser.CreateIndex{Concurrent: concurrent}, TxImplicit, true, "25001 CREATE INDEX CONCURRENTLY cannot run inside a transaction block"},
		{&parser.DropIndex{Concurrent: concurrent}, TxActive, true, "25001 DROP INDEX CONCURRENTLY cannot run inside a transaction block"},
		{&parser.DropIndex{Concurrent: concurrent}, TxImplicit, false, ""},
		{&parser.CreateIndex{}, TxActive, true, "0A000 statement is unsupported in an explicit transaction"},
		{&parser.CreateRole{}, TxActive, true, "0A000 statement is unsupported in an explicit transaction"},
		{&parser.CreateRole{}, TxImplicit, true, ""},
		{&parser.CreateRole{}, TxIdle, false, ""},
	} {
		if got := sqlText(ExplicitUtilityRefusal(tc.stmt, tc.state, tc.inBlock)); got != tc.want {
			t.Fatalf("%T state %d inBlock %v: got %q", tc.stmt, tc.state, tc.inBlock, got)
		}
	}
}

func TestStatementPhases(t *testing.T) {
	call := &parser.Call{}
	if !SnapshotStatement(call) || !ReplacementRead(call) || SnapshotTargetAdmission(call) || DescribeBeforeSnapshot(call) != DescribeAnalyzed || DescribeAfterSnapshot(call) || !PreparableBody(call) || !ProtocolPlannedStatement(call) || ProtocolPlainSelect(call) || ProtocolHasRows(call, nil) || !ReadOnlyExempt(call) || ModifyingTag(call) != "" {
		t.Fatal("CALL phase classification")
	}
	if tag, counted, insert, ok := ClassifyCommandTag(call, TxIdle); tag != "CALL" || counted || insert || !ok {
		t.Fatal(tag, counted, insert, ok)
	}
	if _, ddl := EventCommand(call); ddl || TransactionalSchemaUtility(&parser.CreateFunction{Procedure: true}) {
		t.Fatal("CALL expanded DDL admission")
	}
	if !SnapshotStatement(&parser.SettingCall{}) || SnapshotStatement(&parser.CreateTable{}) {
		t.Fatal("snapshot statements")
	}
	if !SnapshotTargetAdmission(&parser.Copy{From: true}) || SnapshotTargetAdmission(&parser.Copy{}) || !SnapshotTargetAdmission(&parser.Merge{}) || SnapshotTargetAdmission(&parser.Select{}) {
		t.Fatal("snapshot target admission")
	}
	if !ReplacementRead(&parser.Truncate{}) || !ReplacementRead(&parser.Comment{Kind: parser.CommentColumn}) || ReplacementRead(&parser.Comment{Kind: parser.CommentSequence}) || ReplacementRead(&parser.CreateTable{}) {
		t.Fatal("replacement reads")
	}
	for stmt, want := range map[parser.Statement]DescribeClass{
		&parser.Notify{}:                   DescribeEmpty,
		&parser.SetConstraints{}:           DescribeEmpty,
		&parser.Vacuum{}:                   DescribeOperation,
		&parser.AlterOwner{Kind: "domain"}: DescribeOperation,
		&parser.AlterOwner{Kind: "table"}:  DescribeAnalyzed,
		&parser.CreateTrigger{}:            DescribeAnalyzed,
		&parser.Select{}:                   DescribeAnalyzed,
	} {
		if got := DescribeBeforeSnapshot(stmt); got != want {
			t.Fatalf("%T %+v: got %d", stmt, stmt, got)
		}
	}
	if !DescribeAfterSnapshot(&parser.CreateTrigger{}) || DescribeAfterSnapshot(&parser.Select{}) {
		t.Fatal("describe after snapshot")
	}
}
