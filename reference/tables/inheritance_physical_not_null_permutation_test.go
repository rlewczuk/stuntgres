package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent literal program, not the production formatter's output. The
// selected NN is one member of a genuine two-key primary, never an assumed name.
func physicalNotNullCompositeCleanupSQL() string {
	return `WITH target AS (
SELECT nn.oid FROM pg_catalog.pg_constraint AS nn
WHERE nn.conrelid = 'public.c'::regclass AND nn.contype = 'n'
AND nn.conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a')]
AND nn.conislocal = true AND nn.coninhcount = 0 AND nn.conparentid = 0
AND nn.convalidated = true AND nn.connoinherit = false
AND EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid = 'public.c'::regclass AND conname = 'c_key' AND contype = 'p' AND conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a'),(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'b')])
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend WHERE refclassid = 'pg_catalog.pg_constraint'::regclass AND refobjid = nn.oid)
), removed_dependencies AS (
DELETE FROM pg_catalog.pg_depend WHERE classid = 'pg_catalog.pg_constraint'::regclass AND objid IN (SELECT oid FROM target) RETURNING objid
)
DELETE FROM pg_catalog.pg_constraint WHERE oid IN (SELECT oid FROM target)`
}

func TestReferenceInheritanceAlterPhysicalNotNullPermutedForeignKey(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		if s.b.PG {
			s.exec("CREATE TABLE c(a BIGINT,b BIGINT,x BIGINT,y BIGINT)", 0)
			s.exec("CREATE UNIQUE INDEX c_key ON c(a,b) INCLUDE(x)", 0)
			s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key", 0)
			s.exec(physicalNotNullCompositeCleanupSQL(), 1)
		} else {
			physicalNotNullRestoreConstruction(s, "CREATE TABLE public.c(a BIGINT,b BIGINT,x BIGINT,y BIGINT); CREATE UNIQUE INDEX c_key ON public.c(a,b) INCLUDE(x); ALTER TABLE public.c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key;"+physicalNotNullCompositeCleanupSQL()+";")
		}
		s.exec("ALTER TABLE c ADD CONSTRAINT self_fk FOREIGN KEY(x,y) REFERENCES c(b,a)", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT c_also UNIQUE(b,a)", 0)
		s.exec("CREATE UNIQUE INDEX c_index ON c(a,b)", 0)
		s.exec("CREATE VIEW ixview AS SELECT 'c_also'::regclass AS idx", 0)
		s.exec("INSERT INTO c(a,b,x,y) VALUES(1,2,NULL,NULL)", 1)
		s.exec("UPDATE c SET x=2,y=1", 1)
		checksFor := func(payloadType, provider string) []fixture.Step {
			return []fixture.Step{
				fixture.Select("SELECT a,b,x,y FROM c", []string{"a", "b", "x", "y"}, []string{"INT8", "INT8", payloadType, "INT8"}, [][]any{{int64(1), int64(2), int64(2), int64(1)}}),
				fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='self_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (x, y) REFERENCES c(b, a)"}}),
				fixture.Select("SELECT conkey::text,confkey::text,conindid::regclass::text AS provider FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='self_fk'", []string{"conkey", "confkey", "provider"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"{3,4}", "{2,1}", provider}}),
				fixture.Select("SELECT idx::text FROM ixview", []string{"idx"}, []string{"TEXT"}, [][]any{{"c_also"}}),
				fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, [][]any{{"c_b_not_null"}}),
				fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
			}
		}
		enforcement := []fixture.Step{
			fixture.ImplicitError("INSERT INTO c(a,b,x,y) VALUES(NULL,3,NULL,NULL)", inheritanceNullDiagnostic("c", "a", "null, 3, null, null")),
			fixture.ImplicitError("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact()),
			fixture.ImplicitError("INSERT INTO c(a,b,x,y) VALUES(3,4,99,1)", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "c" violates foreign key constraint "self_fk"`, Detail: `Key (x, y)=(99, 1) is not present in table "c".`, Schema: "public", Table: "c", Constraint: "self_fk"}),
		}
		// Before the payload rewrite, the genuine permuted FK still uses the
		// INCLUDE-bearing primary chosen before the competing indexes existed.
		checks := checksFor("INT8", "c_key")
		physicalNotNullControlRoundTrip(s, checks, enforcement)
		// PostgreSQL 18 reselects the later exact-order unique provider while
		// rebuilding the FK for this TYPE. Preserve both independently asserted
		// states and both genuine native/captured-PostgreSQL roundtrips.
		s.exec("ALTER TABLE c ALTER x TYPE INT", 0)
		checks = checksFor("INT4", "c_also")
		physicalNotNullControlRoundTrip(s, checks, enforcement)
		if !s.b.PG {
			// A pre-existing competing provider would make explicit permuted
			// REFERENCES choose it in PostgreSQL. Private replay must not force
			// a different provider merely because a caller supplies the shape.
			forged := "CREATE TABLE public.c(a BIGINT,b BIGINT,x BIGINT,y BIGINT,CONSTRAINT c_also UNIQUE(b,a)); CREATE UNIQUE INDEX c_key ON public.c(a,b) INCLUDE(x); ALTER TABLE public.c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key;" + physicalNotNullCompositeCleanupSQL() + "; ALTER TABLE ONLY public.c ADD CONSTRAINT self_fk FOREIGN KEY(x,y) REFERENCES public.c(b,a);"
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(forged)); err == nil {
				s.t.Fatal("permuted FK bypassed actual competing provider")
			}
			for _, step := range checks {
				s.run(step)
			}
		}
	})
}
