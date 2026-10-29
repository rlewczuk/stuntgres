package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterNoInheritControls(t *testing.T) {
	for _, kind := range []string{"children", "childless", "quoted_childless", "primary_key_dependency"} {
		t.Run(kind, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE blocker(x INT CONSTRAINT p_a_not_null NOT NULL,y INT CONSTRAINT p_a_not_null1 NOT NULL)", 0)
				s.exec("CREATE TABLE p(a INT)", 0)
				s.exec("CREATE TABLE initial_child() INHERITS(p)", 0)
				s.exec("ALTER TABLE ONLY p ALTER a SET NOT NULL", 0)
				inheritanceAlterFacts(s, "p", [][]any{{"p_a_not_null2", true, int64(0), true}})
				capturedName, capturedSQL := "captured_nn", "captured_nn"
				if kind == "quoted_childless" {
					capturedName, capturedSQL = "captured NN", "\"captured NN\""
				}
				s.exec("ALTER TABLE ONLY p RENAME CONSTRAINT p_a_not_null2 TO "+capturedSQL, 0)
				if kind != "children" {
					s.exec("DROP TABLE initial_child", 0)
				}
				if kind == "primary_key_dependency" {
					s.fail("ALTER TABLE p ADD CONSTRAINT p_key PRIMARY KEY(a)", fixture.Diagnostic{
						Code: "55000", Message: `cannot create primary key on column "a"`,
						Detail: `The constraint "captured_nn" on column "a" of table "p", marked NO INHERIT, is incompatible with a primary key.`,
						Hint:   "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT.",
					})
					inheritanceAlterFacts(s, "p", [][]any{{"captured_nn", true, int64(0), true}})
					s.absent("p_key")
					s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, nil)
					s.exec("CREATE TABLE c(k INT UNIQUE) INHERITS(p)", 0)
					s.exec("INSERT INTO c(a,k) VALUES(NULL,1)", 1)
					s.exec("ALTER TABLE p ADD CONSTRAINT p_fk FOREIGN KEY(a) REFERENCES c(k)", 0)
					// A distinct legitimate NN/PK pair retains their shared validated local
					// fact. Both graphs include a real parent-FK-to-descendant dependency.
					s.exec("CREATE TABLE compatible(a INT CONSTRAINT compatible_nn NOT NULL,CONSTRAINT compatible_key PRIMARY KEY(a))", 0)
					s.exec("CREATE TABLE compatible_child(k INT UNIQUE) INHERITS(compatible)", 0)
					s.exec("INSERT INTO compatible_child(a,k) VALUES(2,1)", 1)
					s.exec("ALTER TABLE compatible ADD CONSTRAINT compatible_fk FOREIGN KEY(a) REFERENCES compatible_child(k)", 0)
					s.exec("INSERT INTO compatible VALUES(1)", 1)
				}
				s.exec("INSERT INTO p VALUES(1)", 1)
				facts := [][]any{{capturedName, true, int64(0), true}}
				if kind == "primary_key_dependency" {
					facts = append(facts, []any{"p_fk", true, int64(0), true})
				}
				checks := []fixture.Step{
					fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='p'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, facts),
					fixture.Select("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}),
				}
				if kind == "children" {
					checks = append(checks, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='initial_child'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
				}
				if kind == "primary_key_dependency" {
					checks = append(checks,
						fixture.Select("SELECT a,k FROM ONLY c", []string{"a", "k"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(1)}}),
						fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='compatible'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"compatible_fk", true, int64(0), true}, {"compatible_key", true, int64(0), true}, {"compatible_nn", true, int64(0), false}}),
						fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='compatible_child'::regclass::oid AND contype='n'", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"compatible_nn", false, int64(1), false}}),
						fixture.Select("SELECT a FROM ONLY compatible", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}),
						fixture.Select("SELECT a,k FROM ONLY compatible_child", []string{"a", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}),
					)
				}
				previousNN := inheritanceAlterConstraintOID(s, "p", capturedName)
				inheritanceMutationRoundTrip(s, checks)
				if !s.b.PG && inheritanceAlterConstraintOID(s, "p", capturedName) == previousNN {
					s.t.Fatal("actual replacement reused retired NN identity")
				}
				s.fail("INSERT INTO p VALUES(NULL)", inheritanceNullDiagnostic("p", "a", "null"))
				if !s.b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
						s.t.Fatal(err)
					}
					// Replay accepts only the emitted single qualified ONLY
					// NO INHERIT action. Catalog UPDATE has no legacy shim.
					invalid := []string{
						dump.String() + "\nALTER TABLE ONLY public.p ALTER CONSTRAINT forged_nn NO INHERIT;",
						dump.String() + "\nALTER TABLE ONLY public.p ALTER CONSTRAINT " + capturedSQL + " INHERIT;",
						dump.String() + "\nALTER TABLE public.p ALTER CONSTRAINT " + capturedSQL + " NO INHERIT;",
						dump.String() + "\nALTER TABLE IF EXISTS ONLY public.p ALTER CONSTRAINT " + capturedSQL + " NO INHERIT;",
						dump.String() + "\nALTER TABLE ONLY public.p ALTER CONSTRAINT " + capturedSQL + " NO INHERIT, ALTER CONSTRAINT " + capturedSQL + " NO INHERIT;",
						dump.String() + "\nUPDATE pg_catalog.pg_constraint SET connoinherit=true WHERE FALSE;",
						dump.String() + "\nALTER TABLE ONLY public.p ALTER COLUMN a SET NOT NULL;",
					}
					for caseIndex, text := range invalid {
						if text == dump.String() {
							s.t.Fatalf("forged fixture did not change the valid stream: case=%d kind=%s", caseIndex, kind)
						}
						if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(text)); err == nil {
							s.t.Fatalf("noncanonical constraint-mode replay restored: case=%d kind=%s", caseIndex, kind)
						}
						for _, step := range checks {
							s.run(step)
						}
					}
					step := fixture.ImplicitError("UPDATE pg_catalog.pg_constraint SET connoinherit=true WHERE FALSE", fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					step.ErrorStage = "execute"
					if s.prepared {
						step.ErrorStage = "prepare"
					}
					s.run(step)
				}
				// A newly attached descendant must still inherit neither the captured NN
				// nor the parent's physical FK. This also witnesses the childless fact.
				s.exec("CREATE TABLE later() INHERITS(p)", 0)
				inheritanceAlterFacts(s, "later", nil)
				s.exec("INSERT INTO later VALUES(NULL)", 1)
			})
		})
	}
}
