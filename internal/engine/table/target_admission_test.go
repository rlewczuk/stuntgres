package table

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func admissionStatement(t *testing.T, sql string) parser.Statement {
	t.Helper()
	statements, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(sql, err)
	}
	return statements[0]
}

func targetName(target Target) []string {
	var parts []string
	for _, part := range target.Name.Parts {
		parts = append(parts, part.Text)
	}
	return parts
}

func TestTableTargetAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want Target
		name []string
	}{
		{"CREATE TABLE c PARTITION OF s.p DEFAULT", Target{Mode: lock.AccessExclusive, OwnerOnly: true}, []string{"s", "p"}},
		{"CREATE INDEX ON t(a)", Target{Mode: lock.Share, OwnerOnly: true}, []string{"t"}},
		{"CREATE INDEX CONCURRENTLY ON t(a)", Target{Mode: lock.ShareUpdateExclusive, OwnerOnly: true}, []string{"t"}},
		{"ALTER TABLE IF EXISTS t OWNER TO r", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: true}, []string{"t"}},
		{"COMMENT ON TABLE t IS 'x'", Target{Mode: lock.ShareUpdateExclusive}, []string{"t"}},
		{"COMMENT ON COLUMN s.t.a IS 'x'", Target{Mode: lock.ShareUpdateExclusive}, []string{"s", "t"}},
		{"ALTER TABLE t DROP COLUMN a", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER TABLE t ATTACH PARTITION c DEFAULT", Target{Mode: lock.ShareUpdateExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER TABLE t DETACH PARTITION c", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER TABLE t OF ty", Target{Mode: lock.AccessExclusive, OwnerOnly: true}, []string{"t"}},
		{"ALTER TABLE IF EXISTS t RENAME CONSTRAINT a TO b", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: true}, []string{"t"}},
		{"ALTER TABLE t RENAME COLUMN a TO b", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER TABLE t SET SCHEMA s", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, []string{"t"}},
		{"ALTER TABLE t RENAME TO u", Target{Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, TableRename: true}, []string{"t"}},
	} {
		stmt := admissionStatement(t, tc.sql)
		target, ok, err := TargetAdmission(stmt, true)
		if err != nil || !ok {
			t.Fatalf("%s: ok = %v, err = %v", tc.sql, ok, err)
		}
		if !slices.Equal(targetName(target), tc.name) {
			t.Fatalf("%s: name = %v", tc.sql, targetName(target))
		}
		target.Name = parser.ObjectName{}
		if !reflect.DeepEqual(target, tc.want) {
			t.Fatalf("%s: target = %+v, want %+v", tc.sql, target, tc.want)
		}
		if _, ok, err := TargetAdmission(stmt, false); ok || err != nil {
			t.Fatalf("%s: prepared admission = %v, %v", tc.sql, ok, err)
		}
	}
	for _, sql := range []string{"CREATE TABLE t(a int)", "ALTER TYPE ty OWNER TO r", "COMMENT ON SCHEMA s IS 'x'", "SELECT 1", "INSERT INTO t VALUES (1)", "CREATE RULE r AS ON INSERT TO t DO NOTHING"} {
		if _, ok, err := TargetAdmission(admissionStatement(t, sql), true); ok || err != nil {
			t.Fatalf("%s: admitted = %v, %v", sql, ok, err)
		}
	}
	// NumericOnly ownership has no relation target, even when its OID text is
	// invalid or either semantic operand is absent. Resolution belongs to the
	// ownership executor so role-before-OID diagnostic ordering is preserved.
	for _, sql := range []string{
		"ALTER LARGE OBJECT 910201 OWNER TO missing_owner",
		"ALTER LARGE OBJECT 0 OWNER TO missing_owner",
		"ALTER LARGE OBJECT 1.0 OWNER TO missing_owner",
		"ALTER LARGE OBJECT 4294967296 OWNER TO CURRENT_USER",
	} {
		stmt := admissionStatement(t, sql)
		for _, execute := range []bool{false, true} {
			target, ok, err := TargetAdmission(stmt, execute)
			if ok || err != nil || !reflect.DeepEqual(target, Target{}) {
				t.Fatalf("%s execute=%t: target=%+v admitted=%v error=%v", sql, execute, target, ok, err)
			}
		}
	}
	_, ok, err := TargetAdmission(admissionStatement(t, "COMMENT ON COLUMN a IS 'x'"), true)
	var diagnostic *sqlstate.Error
	if ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "column name must be qualified" {
		t.Fatalf("unqualified column comment = %v, %v", ok, err)
	}
}

func TestAdmitTargetAsksEachEntry(t *testing.T) {
	declining := func(parser.Statement, bool) (Target, bool, error) { return Target{}, false, nil }
	failure := errors.New("entry failed")
	failing := func(parser.Statement, bool) (Target, bool, error) { return Target{}, false, failure }
	stmt := admissionStatement(t, "ALTER TABLE t OF ty")
	if target, ok, err := AdmitTarget([]TargetEntry{declining, TargetAdmission}, stmt, true); !ok || err != nil || target.Mode != lock.AccessExclusive {
		t.Fatalf("admitted = %+v, %v, %v", target, ok, err)
	}
	if _, ok, err := AdmitTarget([]TargetEntry{failing, TargetAdmission}, stmt, true); ok || err != failure {
		t.Fatalf("failure = %v, %v", ok, err)
	}
	if _, ok, err := AdmitTarget([]TargetEntry{declining}, stmt, true); ok || err != nil {
		t.Fatalf("declined = %v, %v", ok, err)
	}
}
