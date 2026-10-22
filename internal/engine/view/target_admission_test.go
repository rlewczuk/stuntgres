package view

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestViewTargetAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want table.Target
		name []string
	}{
		{"CREATE RULE r AS ON INSERT TO s.t DO NOTHING", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"s", "t"}},
		{"ALTER RULE r ON t RENAME TO q", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"DROP RULE IF EXISTS r ON t", table.Target{Mode: lock.AccessExclusive, Rename: true, IfExists: true}, []string{"t"}},
		{"CREATE POLICY p ON t USING (true)", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER POLICY p ON t USING (true)", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"DROP POLICY p ON t", table.Target{Mode: lock.AccessExclusive, Rename: true}, []string{"t"}},
		{"CREATE OR REPLACE VIEW v AS SELECT 1", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, ViewReplacement: true}, []string{"v"}},
		{"ALTER VIEW IF EXISTS v RENAME TO w", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: true, TableRename: true}, []string{"v"}},
		{"ALTER VIEW v OWNER TO r", table.Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"v"}},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		target, ok, err := TargetAdmission(statements[0], true)
		if err != nil || !ok {
			t.Fatalf("%s: ok = %v, err = %v", tc.sql, ok, err)
		}
		var name []string
		for _, part := range target.Name.Parts {
			name = append(name, part.Text)
		}
		if !slices.Equal(name, tc.name) {
			t.Fatalf("%s: name = %v", tc.sql, name)
		}
		target.Name = parser.ObjectName{}
		if !reflect.DeepEqual(target, tc.want) {
			t.Fatalf("%s: target = %+v, want %+v", tc.sql, target, tc.want)
		}
		if _, ok, err := TargetAdmission(statements[0], false); ok || err != nil {
			t.Fatalf("%s: prepared admission = %v, %v", tc.sql, ok, err)
		}
	}
	for _, sql := range []string{"CREATE VIEW v AS SELECT 1", "ALTER TABLE t ADD COLUMN a int", "SELECT * FROM t"} {
		statements, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(sql, err)
		}
		if _, ok, err := TargetAdmission(statements[0], true); ok || err != nil {
			t.Fatalf("%s: admitted = %v, %v", sql, ok, err)
		}
	}
}
