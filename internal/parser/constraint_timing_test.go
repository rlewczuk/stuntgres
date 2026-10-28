package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestConstraintTimingColumnAttachment(t *testing.T) {
	for _, tc := range []struct{ body, message, token string }{
		{"v INT CHECK(v>0) DEFERRABLE", "misplaced DEFERRABLE clause", "DEFERRABLE"},
		{"v INT NOT NULL NOT DEFERRABLE", "misplaced NOT DEFERRABLE clause", "NOT DEFERRABLE"},
		{"v INT CHECK(v>0) INITIALLY DEFERRED", "misplaced INITIALLY DEFERRED clause", "INITIALLY"},
		{"v INT NOT NULL INITIALLY IMMEDIATE", "misplaced INITIALLY IMMEDIATE clause", "INITIALLY"},
		{"v INT UNIQUE DEFERRABLE DEFERRABLE", "multiple DEFERRABLE/NOT DEFERRABLE clauses not allowed", "DEFERRABLE)"},
		{"v INT PRIMARY KEY NOT DEFERRABLE DEFERRABLE", "multiple DEFERRABLE/NOT DEFERRABLE clauses not allowed", "DEFERRABLE)"},
		{"v INT REFERENCES p INITIALLY IMMEDIATE INITIALLY IMMEDIATE", "multiple INITIALLY IMMEDIATE/DEFERRED clauses not allowed", "INITIALLY IMMEDIATE)"},
		{"v INT UNIQUE NOT DEFERRABLE INITIALLY DEFERRED", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY"},
		{"v INT REFERENCES p INITIALLY DEFERRED NOT DEFERRABLE", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "NOT DEFERRABLE"},
		{"v INT DEFERRABLE UNIQUE", "misplaced DEFERRABLE clause", "DEFERRABLE"},
	} {
		q := `CREATE TABLE "é"(` + tc.body + ")"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal("column attachment must remain utility-stage", q, err)
		}
		column := statements[0].(*CreateTable).Elements[0].Column
		var failed *ColumnConstraint
		for i := range column.Constraints {
			if column.Constraints[i].TimingError != "" {
				failed = &column.Constraints[i]
				break
			}
		}
		position := utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1
		if failed == nil || failed.TimingError != tc.message || failed.TimingPos.Character != position {
			t.Fatal(q, failed, tc.message, position)
		}
		if strings.HasSuffix(tc.body, "DEFERRABLE UNIQUE") && (len(column.Constraints) != 2 || column.Constraints[1].Unique == nil || column.Constraints[1].Unique.Deferrable) {
			t.Fatal("later constraint absorbed orphan", column)
		}
	}
}

func TestConstraintTimingTableProperties(t *testing.T) {
	for _, clause := range []string{"UNIQUE(v)", "PRIMARY KEY(v)", "FOREIGN KEY(v) REFERENCES p", "EXCLUDE USING btree(v WITH =)"} {
		for _, properties := range []string{"DEFERRABLE DEFERRABLE", "INITIALLY DEFERRED INITIALLY DEFERRED", "NOT DEFERRABLE NOT DEFERRABLE", "INITIALLY IMMEDIATE INITIALLY IMMEDIATE"} {
			if _, err := Parse("CREATE TABLE t(v INT,CONSTRAINT c " + clause + " " + properties + ")"); err != nil {
				t.Fatal(clause, properties, err)
			}
		}
		for _, tc := range []struct{ properties, message, token string }{
			{"DEFERRABLE NOT DEFERRABLE", "conflicting constraint properties", "NOT DEFERRABLE"},
			{"INITIALLY DEFERRED INITIALLY IMMEDIATE", "conflicting constraint properties", "INITIALLY IMMEDIATE"},
			{"NOT DEFERRABLE INITIALLY DEFERRED", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY DEFERRED"},
		} {
			q := "CREATE TABLE t(v INT,CONSTRAINT c " + clause + " " + tc.properties + ")"
			statements, err := Parse(q)
			var got *sqlstate.Error
			if statements != nil || !errors.As(err, &got) || got.Code != "42601" || got.Message != tc.message || got.Position != strconv.Itoa(strings.LastIndex(q, tc.token)+1) {
				t.Fatal(q, statements, got, err)
			}
		}
	}
}

func TestConstraintTimingAlterActions(t *testing.T) {
	for _, clause := range []string{"UNIQUE(v)", "PRIMARY KEY(v)", "FOREIGN KEY(v) REFERENCES p", "UNIQUE USING INDEX idx", "PRIMARY KEY USING INDEX idx"} {
		q := "ALTER TABLE t ADD CONSTRAINT c " + clause + " INITIALLY DEFERRED"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		a := statements[0].(*AlterTable).Actions[0]
		var deferrable, deferred bool
		switch {
		case a.Unique != nil:
			deferrable, deferred = a.Unique.Deferrable, a.Unique.InitiallyDeferred
		case a.ForeignKey != nil:
			deferrable, deferred = a.ForeignKey.Deferrable, a.ForeignKey.InitiallyDeferred
		case a.UsingIndex != nil:
			deferrable, deferred = a.UsingIndex.Deferrable, a.UsingIndex.InitiallyDeferred
		}
		if !deferrable || !deferred {
			t.Fatal(q, a)
		}
	}
	for _, tc := range []struct {
		properties                                            string
		deferrable, deferred, defSet, initialSet, enforcement bool
		kind                                                  AlterTableActionKind
	}{
		{"DEFERRABLE", true, false, true, false, false, AlterConstraintTiming},
		{"NOT DEFERRABLE", false, false, true, false, false, AlterConstraintTiming},
		{"INITIALLY DEFERRED", true, true, false, true, false, AlterConstraintTiming},
		{"INITIALLY IMMEDIATE", false, false, false, true, false, AlterConstraintTiming},
		{"DEFERRABLE INITIALLY DEFERRED", true, true, true, true, false, AlterConstraintTiming},
		{"DEFERRABLE NOT ENFORCED", true, false, true, false, true, AlterConstraintTiming},
		{"NOT ENFORCED INITIALLY DEFERRED", true, true, false, true, true, AlterConstraintEnforcement},
	} {
		q := `ALTER TABLE ONLY "é" ALTER CONSTRAINT "f é" ` + tc.properties + ", ALTER CONSTRAINT other ENFORCED"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		actions := statements[0].(*AlterTable).Actions
		a := actions[0]
		if len(actions) != 2 || a.Action != tc.kind || a.Constraint.Text != "f é" || a.Deferrable != tc.deferrable || a.InitiallyDeferred != tc.deferred || a.DeferrabilitySet != tc.defSet || a.InitiallySet != tc.initialSet || a.EnforcementSet != tc.enforcement || a.NotEnforced != tc.enforcement || a.Pos.Character == 0 || a.Constraint.Pos.Character == 0 {
			t.Fatal(q, actions)
		}
	}
	for _, q := range []string{
		"ALTER TABLE t ALTER CONSTRAINT c DEFERRABLE trailing",
		"ALTER TABLE t ALTER CONSTRAINT c INITIALLY \"deferred\"",
		"ALTER TABLE t ALTER CONSTRAINT c DEFERRABLE NOT DEFERRABLE",
		"CREATE TABLE t(v INT UNIQUE DEFERRABLE DEFERRABLE) trailing",
	} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}
