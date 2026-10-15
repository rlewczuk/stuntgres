package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func physicalNotNullTwoColumnSetup(s *inheritanceCase) {
	s.t.Helper()
	s.exec("CREATE TABLE p(b INT,a INT CONSTRAINT nn NOT NULL)", 0)
	s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
	s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY(a)", 0)
	s.exec("INSERT INTO c(a,b) VALUES(2,7)", 1)
	s.exec("ALTER TABLE p DROP COLUMN a", 0)
	s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
}

func TestReferenceInheritanceAlterPhysicalNotNullExistingIndex(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		// USING INDEX localizes an existing inherited NN, so it is not the
		// historical TYPE issuer. Exercise the genuine logical constructor:
		// PostgreSQL catalog CTE versus its exact private native import pair.
		if s.b.PG {
			s.exec("CREATE TABLE p(b INT)", 0)
			s.exec("CREATE TABLE c(a BIGINT) INHERITS(p)", 0)
			s.exec("CREATE UNIQUE INDEX c_key ON c(a) INCLUDE(b)", 0)
			s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key", 0)
			s.exec(physicalNotNullCleanupSQL(), 1)
		} else {
			physicalNotNullRestoreConstruction(s, "CREATE TABLE public.p(b INT); CREATE TABLE public.c(a BIGINT) INHERITS(public.p); CREATE UNIQUE INDEX c_key ON public.c(a) INCLUDE(b); ALTER TABLE public.c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key;"+physicalNotNullCleanupSQL()+";")
		}
		s.exec("INSERT INTO c(a,b) VALUES(12,7)", 1)
		s.exec("CREATE TABLE child() INHERITS(c)", 0)
		s.exec("INSERT INTO child(a,b) VALUES(NULL,9)", 1)
		checks := []fixture.Step{
			fixture.Select("SELECT a,b FROM ONLY c", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(12), int64(7)}}),
			fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, nil),
			fixture.Select("SELECT pg_get_indexdef('c_key'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX c_key ON public.c USING btree (a) INCLUDE (b)"}}),
			fixture.Select("SELECT a,b FROM ONLY child", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{nil, int64(9)}}),
			fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='child'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}),
		}
		// pg_index's key/include counts are a PostgreSQL supplemental catalog
		// phase. Shared deparser and mutation checks prove the public distinction
		// without requiring a new native indnkeyatts provider.
		if s.b.PG {
			checks = append(checks, fixture.Select("SELECT indnkeyatts,indnatts FROM pg_index WHERE indexrelid='c_key'::regclass::oid", []string{"indnkeyatts", "indnatts"}, []string{"INT2", "INT2"}, [][]any{{int64(1), int64(2)}}))
		}
		enforcement := []fixture.Step{
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,8)", inheritanceNullDiagnostic("c", "a", "8, null")),
			fixture.ImplicitError("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact()),
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(12,8)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_key"`, Detail: "Key (a)=(12) already exists.", Schema: "public", Table: "c", Constraint: "c_key"}),
			fixture.Exec("INSERT INTO c(a,b) VALUES(13,NULL)", 1),
			fixture.Exec("DELETE FROM ONLY c WHERE a=13", 1),
		}
		physicalNotNullControlRoundTrip(s, checks, enforcement)
		// Payload is not a key or a not-null authority. Its genuine inherited
		// TYPE rebuild still replaces the captured primary/index provider.
		s.exec("ALTER TABLE p ALTER b TYPE BIGINT", 0)
		checks[0] = fixture.Select("SELECT a,b FROM ONLY c", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(12), int64(7)}})
		checks[4] = fixture.Select("SELECT a,b FROM ONLY child", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{nil, int64(9)}})
		physicalNotNullControlRoundTrip(s, checks, enforcement)
	})
}

func TestReferenceInheritanceAlterPhysicalNotNullIndependentPrimary(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		physicalNotNullTwoColumnSetup(s)
		s.exec("ALTER TABLE c DROP CONSTRAINT c_key", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY(b)", 0)
		checks := []fixture.Step{
			fixture.Select("SELECT a,b FROM ONLY c", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(12), int64(7)}}),
			fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 ORDER BY attname", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, [][]any{{"a", true}, {"b", true}}),
			fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, [][]any{{"c_b_not_null"}}),
			fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='p'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (b)"}}),
		}
		enforcement := []fixture.Step{
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,8)", inheritanceNullDiagnostic("c", "a", "8, null")),
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(13,NULL)", inheritanceNullDiagnostic("c", "b", "null, 13")),
			fixture.ImplicitError("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact()),
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(13,7)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_key"`, Detail: "Key (b)=(7) already exists.", Schema: "public", Table: "c", Constraint: "c_key"}),
		}
		physicalNotNullControlRoundTrip(s, checks, enforcement)
		s.exec("INSERT INTO c(a,b) VALUES(12,8)", 1)
		s.values("SELECT a,b FROM ONLY c ORDER BY b", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(12), int64(7)}, {int64(12), int64(8)}})
	})
}
