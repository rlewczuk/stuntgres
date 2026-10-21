package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintCompatibility(t *testing.T) {
	// Release18.6 CreateInheritance/MergeAttributesIntoExisting diagnostics:
	// omitted fields independently mean absent, including Position/object fields.
	cases := []struct {
		name, parent, child, code, message, detail string
	}{
		{"Missing", "a INT", "b INT", "42804", `child table is missing column "a"`, ""},
		{"Type", "a INT", "a BIGINT", "42804", `child table "c" has different type for column "a"`, ""},
		{"Typmod", "a VARCHAR(3)", "a VARCHAR(4)", "42804", `child table "c" has different type for column "a"`, ""},
		{"Collation", `a TEXT COLLATE "C"`, `a TEXT COLLATE "POSIX"`, "42P21", `child table "c" has different collation for column "a"`, ""},
		{"NotNull", "a INT CONSTRAINT pn NOT NULL", "a INT", "42804", `column "a" in child table "c" must be marked NOT NULL`, ""},
		{"MissingCheck", "a INT,CONSTRAINT ck CHECK(a>0)", "a INT", "42804", `child table is missing constraint "ck"`, ""},
		{"DifferentCheck", "a INT,CONSTRAINT ck CHECK(a>0)", "a INT,CONSTRAINT ck CHECK(a>1)", "42804", `child table "c" has different definition for check constraint "ck"`, ""},
		{"CommutedCheck", "a INT,CONSTRAINT ck CHECK(a>0)", "a INT,CONSTRAINT ck CHECK(0<a)", "42804", `child table "c" has different definition for check constraint "ck"`, ""},
		{"NoInheritCheck", "a INT,CONSTRAINT ck CHECK(a>0)", "a INT,CONSTRAINT ck CHECK(a>0) NO INHERIT", "42P17", `constraint "ck" conflicts with non-inherited constraint on child table "c"`, ""},
		{"InvalidCheck", "a INT,CONSTRAINT ck CHECK(a>0)", "a INT", "42P17", `constraint "ck" conflicts with NOT VALID constraint on child table "c"`, ""},
		{"InvalidNN", "a INT CONSTRAINT pn NOT NULL", "a INT", "42P17", `constraint "cn" conflicts with NOT VALID constraint on child table "c"`, ""},
		{"GeneratedRequired", "a INT,g INT GENERATED ALWAYS AS(a+1) STORED", "a INT,g INT", "42804", `column "g" in child table must be a generated column`, ""},
		{"GeneratedForbidden", "a INT,g INT", "a INT,g INT GENERATED ALWAYS AS(a+1) STORED", "42804", `column "g" in child table must not be a generated column`, ""},
		{"GeneratedKind", "a INT,g INT GENERATED ALWAYS AS(a+1) STORED", "a INT,g INT GENERATED ALWAYS AS(a+1) VIRTUAL", "42804", `column "g" inherits from generated column of different kind`, "Parent column is STORED, child column is VIRTUAL."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p("+tc.parent+")", 0)
				s.exec("CREATE TABLE c("+tc.child+")", 0)
				if tc.name == "InvalidCheck" {
					s.exec("ALTER TABLE c ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0)
				}
				if tc.name == "InvalidNN" {
					s.exec("ALTER TABLE c ADD CONSTRAINT cn NOT NULL a NOT VALID", 0)
				}
				// A preexisting row survives rejected attachment; column b exists
				// only in the missing-column case. Other cases use shared a.
				column := "a"
				if tc.name == "Missing" {
					column = "b"
				}
				value := any(2)
				typ := "INT4"
				if tc.name == "Typmod" || tc.name == "Collation" {
					value, typ = "2", "VARCHAR"
					if tc.name == "Collation" {
						typ = "TEXT"
					}
				}
				if tc.name == "Type" {
					typ = "INT8"
				}
				s.exec("INSERT INTO c("+column+") VALUES($1)", 1, value)
				s.fail("ALTER TABLE c INHERIT p", fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
				s.edges("c")
				s.values("SELECT inhseqno FROM pg_inherits WHERE inhrelid='c'::regclass::oid", []string{"inhseqno"}, []string{"INT4"}, nil)
				s.values("SELECT attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attinhcount"}, []string{"INT2"}, func() [][]any {
					if tc.name == "GeneratedRequired" || tc.name == "GeneratedForbidden" || tc.name == "GeneratedKind" {
						return [][]any{{int64(0)}, {int64(0)}}
					}
					return [][]any{{int64(0)}}
				}())
				want := any(int64(2))
				if typ == "TEXT" || typ == "VARCHAR" {
					want = "2"
				}
				s.values("SELECT "+column+" FROM c", []string{column}, []string{typ}, [][]any{{want}})
			})
		})
	}
	t.Run("DuplicateCycleAndRetry", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(b INT)", 0)
			s.fail("ALTER TABLE c INHERIT p", fixture.Diagnostic{Code: "42804", Message: `child table is missing column "a"`})
			s.exec("ALTER TABLE c ADD a INT", 0)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.fail("ALTER TABLE c INHERIT p", fixture.Diagnostic{Code: "42P07", Message: `relation "p" would be inherited from more than once`})
			s.exec("ALTER TABLE p ADD b INT", 0)
			s.fail("ALTER TABLE p INHERIT c", fixture.Diagnostic{Code: "42P07", Message: "circular inheritance not allowed", Detail: `"c" is already a child of "p".`})
			s.edges("c", "p")
			s.edges("p")
			s.values("SELECT inhseqno FROM pg_inherits WHERE inhrelid='c'::regclass::oid", []string{"inhseqno"}, []string{"INT4"}, [][]any{{int64(1)}})
		})
	})
	t.Run("ParentNoInheritIgnored", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0) NO INHERIT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("INSERT INTO c VALUES(-1)", 1)
			s.exec("ALTER TABLE c INHERIT p", 0)
			inheritanceAlterFacts(s, "c", nil)
			s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(-1)}})
		})
	})
}
