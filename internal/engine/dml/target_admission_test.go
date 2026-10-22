package dml

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func TestDMLTargetAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		mode lock.Mode
		name []string
	}{
		{"SELECT * FROM s.t", lock.AccessShare, []string{"s", "t"}},
		{"SELECT * FROM t FOR UPDATE", lock.RowShare, []string{"t"}},
		{"COPY t TO STDOUT", lock.AccessShare, []string{"t"}},
		{"COPY t FROM STDIN", lock.RowExclusive, []string{"t"}},
		{"INSERT INTO t VALUES (1)", lock.RowExclusive, []string{"t"}},
		{"MERGE INTO t USING u ON true WHEN MATCHED THEN DELETE", lock.RowExclusive, []string{"t"}},
		{"DELETE FROM t", lock.RowExclusive, []string{"t"}},
		{"UPDATE t SET a = 1", lock.RowExclusive, []string{"t"}},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		for _, execute := range []bool{true, false} {
			target, ok, err := TargetAdmission(statements[0], execute)
			if err != nil || !ok {
				t.Fatalf("%s: ok = %v, err = %v", tc.sql, ok, err)
			}
			var name []string
			for _, part := range target.Name.Parts {
				name = append(name, part.Text)
			}
			if !slices.Equal(name, tc.name) || target.Mode != tc.mode || target.OwnerOnly || target.Rename || target.IfExists || target.TableRename || target.ViewReplacement {
				t.Fatalf("%s: target = %+v", tc.sql, target)
			}
		}
	}
	for _, sql := range []string{"SELECT 1", "CREATE TABLE t(a int)"} {
		statements, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(sql, err)
		}
		if _, ok, err := TargetAdmission(statements[0], true); ok || err != nil {
			t.Fatalf("%s: admitted = %v, %v", sql, ok, err)
		}
	}
}
