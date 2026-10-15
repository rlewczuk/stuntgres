package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterPhysicalNotNullSelfForeignKey(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		// Real PostgreSQL constructor versus its deliberately private native
		// counterpart. No public native catalog-DML permission is implied.
		if s.b.PG {
			s.exec("CREATE TABLE c(a BIGINT,b BIGINT)", 0)
			s.exec("CREATE UNIQUE INDEX c_key ON c(a) INCLUDE(b)", 0)
			s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key", 0)
			s.exec(physicalNotNullCleanupSQL(), 1)
		} else {
			physicalNotNullRestoreConstruction(s, "CREATE TABLE public.c(a BIGINT,b BIGINT); CREATE UNIQUE INDEX c_key ON public.c(a) INCLUDE(b); ALTER TABLE public.c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key;"+physicalNotNullCleanupSQL()+";")
		}
		// At replay this inline UNIQUE precedes the deferred captured primary.
		// The FK must still bind the real PRIMARY, not merely a same-key index.
		s.exec("ALTER TABLE c ADD CONSTRAINT c_also UNIQUE(a)", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT self_fk FOREIGN KEY(b) REFERENCES c", 0)
		s.exec("INSERT INTO c(a,b) VALUES(12,12)", 1)
		checks := []fixture.Step{
			fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(12), int64(12)}}),
			fixture.Select("SELECT conname,conindid::regclass::text AS provider FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype IN('p','f','u') ORDER BY conname", []string{"conname", "provider"}, []string{"NAME", "TEXT"}, [][]any{{"c_also", "c_also"}, {"c_key", "c_key"}, {"self_fk", "c_key"}}),
			fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, nil),
			fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
		}
		enforcement := []fixture.Step{
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,12)", inheritanceNullDiagnostic("c", "a", "null, 12")),
			fixture.ImplicitError("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact()),
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(13,14)", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "c" violates foreign key constraint "self_fk"`, Detail: `Key (b)=(14) is not present in table "c".`, Schema: "public", Table: "c", Constraint: "self_fk"}),
		}
		physicalNotNullControlRoundTrip(s, checks, enforcement)
		if s.b.PG {
			return
		}
		prefix := "CREATE TABLE public.c(a BIGINT,b BIGINT,CONSTRAINT c_also UNIQUE(a)); CREATE UNIQUE INDEX c_key ON public.c(a) INCLUDE(b); ALTER TABLE public.c ADD CONSTRAINT c_key PRIMARY KEY USING INDEX c_key;" + physicalNotNullCleanupSQL() + ";"
		for label, tail := range map[string]string{
			"wrong provider explicit keys": "ALTER TABLE ONLY public.c ADD CONSTRAINT self_fk FOREIGN KEY(b) REFERENCES public.c(a);",
			"wrong target":                 "ALTER TABLE ONLY public.c ADD CONSTRAINT self_fk FOREIGN KEY(b) REFERENCES public.other;",
			"expired provider":             "CREATE INDEX unrelated ON public.c(b); ALTER TABLE ONLY public.c ADD CONSTRAINT self_fk FOREIGN KEY(b) REFERENCES public.c;",
		} {
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(prefix+tail)); err == nil {
				s.t.Fatal("forged physical FK provider accepted", label)
			}
			for _, step := range checks {
				s.run(step)
			}
		}
	})
}
