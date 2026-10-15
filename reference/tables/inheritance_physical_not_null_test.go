package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func physicalNotNullMissingFact() fixture.Diagnostic {
	return fixture.Diagnostic{Code: "XX000", Message: `cache lookup failed for not-null constraint on column "a" of relation "c"`}
}

func physicalNotNullFlags(s *inheritanceCase, kind string) {
	s.t.Helper()
	s.values("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}})
	s.values("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, nil)
	s.values("SELECT a FROM ONLY c ORDER BY a", []string{"a"}, []string{kind}, [][]any{{int64(12)}})
}

func TestReferenceInheritanceAlterPhysicalNotNullTransitions(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY(a)", 0)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
		physicalNotNullFlags(s, "INT8")
		s.fail("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact())
		physicalNotNullFlags(s, "INT8")
		s.exec("ALTER TABLE c DROP CONSTRAINT c_key", 0)
		physicalNotNullFlags(s, "INT8")
		s.exec("ALTER TABLE c ALTER a TYPE INT", 0)
		physicalNotNullFlags(s, "INT4")
		s.fail("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact())
		physicalNotNullFlags(s, "INT4")
		physicalNotNullRoundTrip(s, "INT4", false, 1, nil)
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		s.exec("INSERT INTO c VALUES(12)", 1)
		s.values("SELECT a FROM ONLY c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(12)}})
	})
}

// This genuine PostgreSQL catalog write is the closed logical-import boundary,
// not a new public native catalog DELETE route or a guessed generated NN name.
func physicalNotNullCleanupSQL() string {
	return `WITH target AS (
SELECT nn.oid FROM pg_catalog.pg_constraint AS nn
WHERE nn.conrelid = 'public.c'::regclass AND nn.contype = 'n'
AND nn.conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a')]
AND nn.conislocal = true AND nn.coninhcount = 0 AND nn.conparentid = 0
AND nn.convalidated = true AND nn.connoinherit = false
AND EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid = 'public.c'::regclass AND conname = 'c_key' AND contype = 'p' AND conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a')])
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend WHERE refclassid = 'pg_catalog.pg_constraint'::regclass AND refobjid = nn.oid)
), removed_dependencies AS (
DELETE FROM pg_catalog.pg_depend WHERE classid = 'pg_catalog.pg_constraint'::regclass AND objid IN (SELECT oid FROM target) RETURNING objid
)
DELETE FROM pg_catalog.pg_constraint WHERE oid IN (SELECT oid FROM target)`
}

func TestReferenceInheritanceAlterPhysicalNotNullCatalogConstruction(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		var generated uint32
		if s.b.PG {
			s.exec("CREATE TABLE c(a BIGINT,CONSTRAINT c_a_not_null CHECK(a>0))", 0)
			s.exec("ALTER TABLE ONLY c ADD CONSTRAINT c_key PRIMARY KEY(a)", 0)
			s.exec("INSERT INTO c VALUES(12)", 1)
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'").Scan(&generated); err != nil {
				s.t.Fatal(err)
			}
			s.run(fixture.Select("SELECT deptype,refclassid::regclass::text AS provider,refobjid::regclass::text AS relation,refobjsubid FROM pg_depend WHERE classid='pg_constraint'::regclass::oid AND objid=$1::oid ORDER BY refclassid,refobjid,refobjsubid", []string{"deptype", "provider", "relation", "refobjsubid"}, []string{"CHAR", "TEXT", "TEXT", "INT4"}, [][]any{{"a", "pg_class", "c", int64(1)}}, generated))
			s.run(fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid='pg_constraint'::regclass::oid AND refobjid=$1::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, generated))
			s.exec(physicalNotNullCleanupSQL(), 1)
		} else {
			physicalNotNullRestoreConstruction(s, "CREATE TABLE public.c(a BIGINT,CONSTRAINT c_a_not_null CHECK(a>0)); ALTER TABLE ONLY public.c ADD CONSTRAINT c_key PRIMARY KEY(a);"+physicalNotNullCleanupSQL()+"; INSERT INTO public.c VALUES(12);")
		}
		physicalNotNullFlags(s, "INT8")
		if s.b.PG {
			s.run(fixture.Select("SELECT conname FROM pg_constraint WHERE oid=$1::oid", []string{"conname"}, []string{"NAME"}, nil, generated))
			s.run(fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid='pg_constraint'::regclass::oid AND objid=$1::oid) OR (refclassid='pg_constraint'::regclass::oid AND refobjid=$1::oid)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, generated))
		}
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		duplicate := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_key"`, Detail: "Key (a)=(12) already exists.", Schema: "public", Table: "c", Constraint: "c_key"}
		s.fail("INSERT INTO c VALUES(12)", duplicate)
		physicalNotNullFlags(s, "INT8")
		physicalNotNullRoundTrip(s, "INT8", true, 1, nil)
	})
}

func TestReferenceInheritanceAlterPhysicalNotNullNewChild(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY(a)", 0)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
		s.exec("CREATE TABLE child() INHERITS(c)", 0)
		s.values("SELECT attnotnull,attislocal,attinhcount FROM pg_attribute WHERE attrelid='child'::regclass::oid AND attname='a'", []string{"attnotnull", "attislocal", "attinhcount"}, []string{"BOOL", "BOOL", "INT2"}, [][]any{{false, false, int64(1)}})
		s.values("SELECT conname FROM pg_constraint WHERE conrelid='child'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil)
		s.exec("INSERT INTO child VALUES(NULL)", 1)
		s.values("SELECT a FROM ONLY child", []string{"a"}, []string{"INT8"}, [][]any{{nil}})
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		s.values("SELECT a FROM c ORDER BY a NULLS LAST", []string{"a"}, []string{"INT8"}, [][]any{{int64(12)}, {nil}})
		physicalNotNullRoundTrip(s, "INT8", true, 1, []fixture.Step{
			fixture.Select("SELECT attnotnull,attislocal,attinhcount FROM pg_attribute WHERE attrelid='child'::regclass::oid AND attname='a'", []string{"attnotnull", "attislocal", "attinhcount"}, []string{"BOOL", "BOOL", "INT2"}, [][]any{{false, false, int64(1)}}),
			fixture.Select("SELECT a FROM ONLY child", []string{"a"}, []string{"INT8"}, [][]any{{nil}}),
		})
	})
}

func TestReferenceInheritanceAlterPhysicalNotNullKeylessConstruction(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		if s.b.PG {
			s.exec("CREATE TABLE c(a BIGINT)", 0)
			s.exec("ALTER TABLE ONLY c ALTER COLUMN a SET NOT NULL", 0)
			s.values("SELECT connoinherit FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"connoinherit"}, []string{"BOOL"}, [][]any{{false}})
		}
		cleanup := `WITH target AS (
SELECT nn.oid FROM pg_catalog.pg_constraint AS nn
WHERE nn.conrelid = 'public.c'::regclass AND nn.contype = 'n'
AND nn.conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a')]
AND nn.conislocal = true AND nn.coninhcount = 0 AND nn.conparentid = 0
AND nn.convalidated = true AND nn.connoinherit = false
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid = 'public.c'::regclass AND contype = 'p')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend WHERE refclassid = 'pg_catalog.pg_constraint'::regclass AND refobjid = nn.oid)
), removed_dependencies AS (
DELETE FROM pg_catalog.pg_depend WHERE classid = 'pg_catalog.pg_constraint'::regclass AND objid IN (SELECT oid FROM target) RETURNING objid
)
DELETE FROM pg_catalog.pg_constraint WHERE oid IN (SELECT oid FROM target)`
		if s.b.PG {
			s.exec(cleanup, 1)
		} else {
			physicalNotNullRestoreConstruction(s, "CREATE TABLE public.c(a BIGINT); ALTER TABLE ONLY public.c ALTER COLUMN a SET NOT NULL;"+cleanup+";")
		}
		s.exec("INSERT INTO c VALUES(12),(12)", 2)
		s.values("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}})
		s.values("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil)
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT8"}, [][]any{{int64(12)}, {int64(12)}})
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		s.fail("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact())
		physicalNotNullRoundTrip(s, "INT8", false, 2, nil)
	})
}
