package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterRecipeControls(t *testing.T) {
	t.Run("default_authority", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT DEFAULT 1,b INT DEFAULT 7,n INT DEFAULT 11)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("ALTER TABLE ONLY c ALTER a SET DEFAULT 9", 0)
			s.exec("ALTER TABLE ONLY c ALTER b DROP DEFAULT", 0)
			s.exec("ALTER TABLE ONLY c ALTER n SET DEFAULT NULL", 0)
			s.exec("ALTER TABLE ONLY p ALTER a SET DEFAULT 2", 0)
			s.exec("ALTER TABLE ONLY p ALTER b SET DEFAULT 8", 0)
			s.exec("ALTER TABLE ONLY p ALTER n SET DEFAULT 12", 0)
			for _, table := range []string{"p", "c", "g"} {
				s.exec("INSERT INTO "+table+" DEFAULT VALUES", 1)
			}
			checks := []fixture.Step{
				fixture.Select("SELECT a,b,n FROM ONLY p", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(8), int64(12)}}),
				fixture.Select("SELECT a,b,n FROM ONLY c", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), nil, nil}}),
				fixture.Select("SELECT a,b,n FROM ONLY g", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}}),
				fixture.Select("SELECT attname,attislocal,attinhcount,atthasdef FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount", "atthasdef"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"a", false, int64(1), true}, {"b", false, int64(1), false}, {"n", false, int64(1), false}}),
				fixture.Select("SELECT attname,attislocal,attinhcount,atthasdef FROM pg_attribute WHERE attrelid='g'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount", "atthasdef"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"a", false, int64(1), true}, {"b", false, int64(1), true}, {"n", false, int64(1), true}}),
			}
			inheritanceMutationRoundTrip(s, checks)
			for _, table := range []string{"p", "c", "g"} {
				s.exec("INSERT INTO "+table+" DEFAULT VALUES", 1)
			}
			s.values("SELECT a,b,n FROM p ORDER BY a,b", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}, {int64(1), int64(7), int64(11)}, {int64(2), int64(8), int64(12)}, {int64(2), int64(8), int64(12)}, {int64(9), nil, nil}, {int64(9), nil, nil}})
		})
	})
	t.Run("default_dependency", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,r REGCLASS)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("ALTER TABLE p ALTER r SET DEFAULT 'c'::regclass", 0)
			s.exec("CREATE TABLE same() INHERITS(p)", 0)
			s.exec("ALTER TABLE ONLY c ALTER r SET DEFAULT 'g'::regclass", 0)
			s.exec("ALTER TABLE ONLY g ALTER r DROP DEFAULT", 0)
			s.exec("INSERT INTO p(a) VALUES(1)", 1)
			s.exec("INSERT INTO c(a) VALUES(2)", 1)
			s.exec("INSERT INTO g(a) VALUES(3)", 1)
			s.exec("INSERT INTO same(a) VALUES(7)", 1)
			checks := []fixture.Step{
				fixture.Select("SELECT a,r::text AS target FROM ONLY p", []string{"a", "target"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "c"}}),
				fixture.Select("SELECT a,r::text AS target FROM ONLY c", []string{"a", "target"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "g"}}),
				fixture.Select("SELECT a,r::text AS target FROM ONLY g", []string{"a", "target"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}}),
				fixture.Select("SELECT a,r::text AS target FROM ONLY same", []string{"a", "target"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "c"}}),
				fixture.Select("SELECT attname,attislocal,attinhcount,atthasdef FROM pg_attribute WHERE attrelid='same'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount", "atthasdef"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"a", false, int64(1), false}, {"r", false, int64(1), true}}),
			}
			inheritanceMutationRoundTrip(s, checks)
			s.exec("INSERT INTO p(a) VALUES(4)", 1)
			s.exec("INSERT INTO c(a) VALUES(5)", 1)
			s.exec("INSERT INTO g(a) VALUES(6)", 1)
			s.exec("INSERT INTO same(a) VALUES(8)", 1)
			s.values("SELECT a,r::text AS target FROM p ORDER BY a", []string{"a", "target"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "c"}, {int64(2), "g"}, {int64(3), nil}, {int64(4), "c"}, {int64(5), "g"}, {int64(6), nil}, {int64(7), "c"}, {int64(8), "c"}})
		})
	})
	for _, kind := range []string{"stored", "stored_override", "virtual", "virtual_override", "virtual_local_check", "virtual_other_parent_check", "virtual_two_recipes", "virtual_check_diamond", "virtual_local_implicit_generation", "virtual_mixed_owner"} {
		t.Run(kind, func(t *testing.T) {
			var replayRoles []string
			if kind == "virtual_mixed_owner" {
				replayRoles = []string{"inherit_recipe_owner"}
			}
			inheritanceScenario(t, func(s *inheritanceCase) {
				mode := "VIRTUAL"
				if kind == "stored" || kind == "stored_override" {
					mode = "STORED"
				}
				hasCheck := strings.Contains(kind, "check") || kind == "virtual_mixed_owner"
				var bootstrapOwner string
				if kind == "virtual_mixed_owner" {
					if err := s.c.QueryRowContext(s.p.Ctx, "SELECT current_user").Scan(&bootstrapOwner); err != nil {
						s.t.Fatal(err)
					}
				}
				s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a+1) "+mode+")", 0)
				if kind == "virtual_check_diamond" {
					s.exec("CREATE TABLE q(a INT,CONSTRAINT positive CHECK(a>0))", 0)
					s.exec("CREATE TABLE l() INHERITS(p,q)", 0)
					s.exec("CREATE TABLE r() INHERITS(p,q)", 0)
					s.exec("CREATE TABLE c(CONSTRAINT positive CHECK(a>0)) INHERITS(l,r)", 0)
				} else if kind == "virtual_other_parent_check" {
					s.exec("CREATE TABLE q(a INT,CONSTRAINT positive CHECK(a>0))", 0)
					s.exec("CREATE TABLE c() INHERITS(q,p)", 0)
				} else if kind == "virtual_two_recipes" {
					s.exec("CREATE TABLE q(a INT,g INT GENERATED ALWAYS AS(a+1) VIRTUAL)", 0)
					s.exec("CREATE TABLE c() INHERITS(q,p)", 0)
				} else if kind == "virtual_local_implicit_generation" {
					s.exec("CREATE TABLE c(g INT) INHERITS(p)", 0)
				} else {
					local := ""
					if kind == "virtual_local_check" || kind == "virtual_mixed_owner" {
						local = "CONSTRAINT positive CHECK(a>0)"
					}
					s.exec("CREATE TABLE c("+local+") INHERITS(p)", 0)
				}
				s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
				s.exec("ALTER TABLE ONLY p ALTER g SET EXPRESSION AS(a+2)", 0)
				if kind == "virtual_mixed_owner" {
					if s.b.PG {
						// The role already exists before native dump-byte replay;
						// this callback's public-schema reset requires fresh USAGE.
						s.exec("GRANT USAGE ON SCHEMA public TO inherit_recipe_owner", 0)
					}
					s.exec("ALTER TABLE c OWNER TO inherit_recipe_owner", 0)
				}
				overridden := strings.HasSuffix(kind, "_override")
				if overridden {
					s.exec("ALTER TABLE ONLY c ALTER g SET EXPRESSION AS(a+100)", 0)
				}
				childValue, futureValue := int64(3), int64(5)
				if overridden {
					childValue, futureValue = 102, 104
				}
				s.exec("CREATE TABLE unrelated(a INT)", 0)
				s.exec("INSERT INTO p(a) VALUES(1)", 1)
				s.exec("INSERT INTO c(a) VALUES(2)", 1)
				s.exec("INSERT INTO leaf(a) VALUES(3)", 1)
				count := int64(1)
				if kind == "virtual_other_parent_check" || kind == "virtual_two_recipes" || kind == "virtual_check_diamond" {
					count = 2
				}
				gcount := int64(1)
				if kind == "virtual_two_recipes" || kind == "virtual_check_diamond" {
					gcount = 2
				}
				kindCode := "v"
				if mode == "STORED" {
					kindCode = "s"
				}
				checks := []fixture.Step{
					fixture.Select("SELECT a,g FROM ONLY p", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}),
					fixture.Select("SELECT a,g FROM ONLY c", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), childValue}}),
					fixture.Select("SELECT a,g FROM ONLY leaf", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}),
					fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", false, count}, {"g", kind == "virtual_local_implicit_generation", gcount}}),
					fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='leaf'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", false, int64(1)}, {"g", false, int64(1)}}),
					fixture.Select("SELECT attgenerated AS kind,atttypmod FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='g'", []string{"kind", "atttypmod"}, []string{"CHAR", "INT4"}, [][]any{{kindCode, int64(-1)}}),
				}
				if hasCheck {
					local := kind != "virtual_other_parent_check"
					inherited := map[bool]int64{true: 0, false: 1}[local]
					if kind == "virtual_check_diamond" {
						inherited = 2
					}
					checks = append(checks,
						fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"positive", local, inherited, false}}),
						fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='leaf'::regclass::oid", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"positive", false, int64(1), false}}),
					)
				}
				if kind == "virtual_check_diamond" {
					for _, table := range []string{"q", "l", "r"} {
						count := int64(1)
						if table == "q" {
							count = 0
						}
						checks = append(checks, fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"positive", table == "q", count, false}}))
					}
				}
				if kind == "virtual_mixed_owner" {
					checks = append(checks, fixture.Select("SELECT c.relname,r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname IN ('p','c','leaf') ORDER BY c.relname", []string{"relname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"c", "inherit_recipe_owner"}, {"leaf", bootstrapOwner}, {"p", bootstrapOwner}}))
				}
				inheritanceMutationRoundTrip(s, checks)
				if !s.b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
						s.t.Fatal(err)
					}
					invalid := []string{dump.String() + "\nALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+999);"}
					if hasCheck {
						definer := "c"
						if kind == "virtual_other_parent_check" || kind == "virtual_check_diamond" {
							definer = "q"
						}
						actual := "ALTER TABLE \"public\".\"" + definer + "\" ADD CONSTRAINT \"positive\""
						if !strings.Contains(dump.String(), actual) {
							s.t.Fatal("actual dump omitted deferred local CHECK definition")
						}
						invalid = append(invalid, strings.Replace(dump.String(), actual, "ALTER TABLE \"public\".\"unrelated\" ADD CONSTRAINT \"positive\"", 1))
					}
					if kind == "virtual_mixed_owner" {
						actual := "ALTER TABLE \"public\".\"c\" OWNER TO \"inherit_recipe_owner\""
						if !strings.Contains(dump.String(), actual) {
							s.t.Fatal("actual dump omitted paired inherited owner restoration")
						}
						invalid = append(invalid,
							strings.Replace(dump.String(), actual, "ALTER TABLE \"public\".\"unrelated\" OWNER TO \"inherit_recipe_owner\"", 1),
							dump.String()+"\n"+actual+";",
							strings.Replace(dump.String(), "ALTER TABLE \"public\".\"c\" ADD CONSTRAINT \"positive\"", "SET SESSION AUTHORIZATION 'inherit_recipe_owner';\nALTER TABLE \"public\".\"c\" ADD CONSTRAINT \"positive\"", 1),
						)
					}
					for _, text := range invalid {
						if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(text)); err == nil {
							s.t.Fatal("unpaired or unrelated recipe replay succeeded")
						}
						for _, step := range checks {
							s.run(step)
						}
					}
				}
				s.exec("INSERT INTO c(a) VALUES(4)", 1)
				s.values("SELECT a,g FROM ONLY c ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), childValue}, {int64(4), futureValue}})
			}, replayRoles...)
		})
	}
}

func TestReferenceInheritanceAlterGeneratedReplaySyntax(t *testing.T) {
	for _, mode := range []string{"STORED", "VIRTUAL"} {
		t.Run(mode, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				// Declaration-context assignment supplies the full VARCHAR(5) Usage.
				// The literal NULL cast is already admitted by declaration binding;
				// this does not imply support for arbitrary nonliteral VARCHAR casts.
				s.exec("CREATE TABLE p(a INT,g VARCHAR(5) GENERATED ALWAYS AS(NULL) "+mode+")", 0)
				s.exec("CREATE TABLE q(a INT,g VARCHAR(5) GENERATED ALWAYS AS(NULL) "+mode+")", 0)
				s.exec("CREATE TABLE c() INHERITS(p,q)", 0)
				explicit := "ALTER TABLE ONLY p ALTER g SET EXPRESSION AS(NULL::VARCHAR(5))"
				s.exec(explicit, 0)
				s.exec("INSERT INTO p(a) VALUES(0)", 1)
				s.values("SELECT a,g FROM ONLY p", []string{"a", "g"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(0), nil}})
				s.values("SELECT atttypmod FROM pg_attribute WHERE attrelid='p'::regclass::oid AND attname='g'", []string{"atttypmod"}, []string{"INT4"}, [][]any{{int64(9)}})
				s.exec("DELETE FROM ONLY p WHERE a=0", 1)
				s.exec("ALTER TABLE ONLY p ALTER g SET EXPRESSION AS((a+2)::text)", 0)
				s.exec("ALTER TABLE ONLY q ALTER g SET EXPRESSION AS((a+10)::text)", 0)
				s.exec("ALTER TABLE ONLY c ALTER g SET EXPRESSION AS((a+1)::text)", 0)
				s.exec("INSERT INTO p(a) VALUES(1)", 1)
				s.exec("INSERT INTO q(a) VALUES(2)", 1)
				s.exec("INSERT INTO c(a) VALUES(3)", 1)
				checks := []fixture.Step{
					fixture.Select("SELECT attname,attnum,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attislocal", "attinhcount"}, []string{"NAME", "INT2", "BOOL", "INT2"}, [][]any{{"a", int64(1), false, int64(2)}, {"g", int64(2), false, int64(2)}}),
					fixture.Select("SELECT a,g FROM ONLY p", []string{"a", "g"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "3"}}),
					fixture.Select("SELECT a,g FROM ONLY q", []string{"a", "g"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(2), "12"}}),
					fixture.Select("SELECT a,g FROM ONLY c", []string{"a", "g"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(3), "4"}}),
				}
				kindCode := "v"
				if mode == "STORED" {
					kindCode = "s"
				}
				for _, table := range []string{"p", "q", "c"} {
					checks = append(checks, fixture.Select("SELECT attgenerated AS kind,atttypmod FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='g'", []string{"kind", "atttypmod"}, []string{"CHAR", "INT4"}, [][]any{{kindCode, int64(9)}}))
				}
				inheritanceMutationRoundTrip(s, checks)
				s.exec("INSERT INTO c(a) VALUES(4)", 1)
				s.values("SELECT a,g FROM ONLY c ORDER BY a", []string{"a", "g"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(3), "4"}, {int64(4), "5"}})
			})
		})
	}
}

// Two disconnected inheritance components retain genuine cross-component
// regclass providers and deferred-default cycles in the actual dumped bytes.
func TestReferenceInheritanceAlterRecipeComponents(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a+1) VIRTUAL,t REGCLASS)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE q(a INT,g REGCLASS GENERATED ALWAYS AS('p'::regclass) VIRTUAL,t REGCLASS)", 0)
		s.exec("CREATE TABLE r() INHERITS(q)", 0)
		s.exec("ALTER TABLE ONLY p ALTER g SET EXPRESSION AS(a+2)", 0)
		s.exec("ALTER TABLE ONLY q ALTER g SET EXPRESSION AS('c'::regclass)", 0)
		s.exec("ALTER TABLE p ALTER t SET DEFAULT 'r'::regclass", 0)
		s.exec("ALTER TABLE q ALTER t SET DEFAULT 'c'::regclass", 0)
		s.exec("INSERT INTO p(a) VALUES(1)", 1)
		s.exec("INSERT INTO c(a) VALUES(2)", 1)
		s.exec("INSERT INTO q(a) VALUES(3)", 1)
		s.exec("INSERT INTO r(a) VALUES(4)", 1)
		checks := []fixture.Step{
			fixture.Select("SELECT a,g,t::text AS target FROM p ORDER BY a", []string{"a", "g", "target"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "r"}, {int64(2), int64(4), "r"}}),
			fixture.Select("SELECT a,g::text AS generated,t::text AS target FROM q ORDER BY a", []string{"a", "generated", "target"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "c", "c"}, {int64(4), "c", "c"}}),
		}
		for _, table := range []string{"c", "r"} {
			checks = append(checks, fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", false, int64(1)}, {"g", false, int64(1)}, {"t", false, int64(1)}}))
		}
		inheritanceMutationRoundTrip(s, checks)
		s.exec("INSERT INTO c(a) VALUES(5)", 1)
		s.exec("INSERT INTO r(a) VALUES(6)", 1)
		s.values("SELECT a,g,t::text AS target FROM ONLY c ORDER BY a", []string{"a", "g", "target"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(3), "r"}, {int64(5), int64(6), "r"}})
		s.values("SELECT a,g::text AS generated,t::text AS target FROM ONLY r ORDER BY a", []string{"a", "generated", "target"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(4), "p", "c"}, {int64(6), "p", "c"}})
	})
}
