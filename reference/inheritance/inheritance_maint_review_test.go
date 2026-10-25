package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The SQL contract is shared. Only native executes actual catalog replacement;
// PostgreSQL independently establishes the surrounding TEMP history semantics.
func TestReferenceInheritanceMaintReviewTemporaryHistory(t *testing.T) {
	for _, replacement := range []string{"Reset", "Restore"} {
		t.Run(replacement, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE maint_temp_marker(a INT)", 0)
				s.exec("INSERT INTO maint_temp_marker VALUES(77)", 1)
				s.exec("CREATE TEMP TABLE maint_temp_p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
				s.exec("CREATE TEMP TABLE maint_temp_c(a INT) INHERITS(maint_temp_p)", 0)
				s.exec("CREATE TEMP TABLE maint_temp_leaf() INHERITS(maint_temp_c)", 0)
				s.exec("INSERT INTO maint_temp_c VALUES(2,20)", 1)
				s.exec("INSERT INTO maint_temp_leaf VALUES(3,30)", 1)
				s.exec("ALTER TABLE maint_temp_p DROP a", 0)
				identities := map[string]int64{}
				for _, name := range []string{"maint_temp_c", "maint_temp_leaf"} {
					for _, constraint := range []string{"ck", "nn"} {
						identities[name+constraint] = inheritanceAlterConstraintOID(s, name, constraint)
					}
				}
				var namespace string
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='maint_temp_c'::regclass::oid").Scan(&namespace); err != nil {
					s.t.Fatal(err)
				}
				observe := func() {
					for _, name := range []string{"maint_temp_c", "maint_temp_leaf"} {
						inheritanceMaintRetiredFacts(s, name, 1)
						for _, constraint := range []string{"ck", "nn"} {
							if got := inheritanceAlterConstraintOID(s, name, constraint); got != identities[name+constraint] {
								s.t.Fatal("retained TEMP constraint identity changed", name, constraint, got, identities[name+constraint])
							}
						}
						null := inheritanceNullDiagnostic(name, "a", "null, null")
						null.Schema = namespace
						check := inheritanceCheckDiagnostic(name, "ck", "0, 0")
						check.Schema = namespace
						s.fail("INSERT INTO "+name+"(a,b) VALUES(NULL,NULL)", null)
						s.fail("INSERT INTO "+name+"(a,b) VALUES(0,0)", check)
					}
					s.values("SELECT a,b FROM maint_temp_c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}})
					s.values("SELECT a,b FROM ONLY maint_temp_c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}})
					inheritanceMaintOrdinals(s, "maint_temp_leaf", [][]any{{"maint_temp_c", int64(1), false}})
				}
				observe()
				if !s.b.PG {
					if replacement == "Reset" {
						if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
							s.t.Fatal(err)
						}
						s.absent("maint_temp_marker")
					} else {
						var dump bytes.Buffer
						if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
							s.t.Fatal(err)
						}
						if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							s.t.Fatal(err)
						}
						s.values("SELECT a FROM maint_temp_marker", []string{"a"}, []string{"INT4"}, [][]any{{int64(77)}})
					}
				}
				observe()
				s.exec("ALTER TABLE maint_temp_c NO INHERIT maint_temp_p", 0)
				inheritanceMaintOrdinals(s, "maint_temp_c", nil)
				inheritanceAlterAttributes(s, "maint_temp_c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), true, int64(0)}})
				inheritanceAlterAttributes(s, "maint_temp_leaf", [][]any{{"a", int64(1), false, int64(1)}, {"b", int64(2), false, int64(1)}})
				observe()
				s.exec("DROP TABLE maint_temp_p", 0)
				s.absent("maint_temp_p")
				observe()
			})
		})
	}
}

func TestReferenceInheritanceMaintReviewErrorOrder(t *testing.T) {
	cases := []struct {
		name, child, query string
		denied             bool
		diagnostic         fixture.Diagnostic
	}{
		{"IncompatibleBeforeMissing", "b", "ALTER TABLE c INHERIT p,INHERIT missing_parent", false, fixture.Diagnostic{Code: "42804", Message: `child table is missing column "a"`}},
		{"IncompatibleBeforeUnauthorized", "b", "ALTER TABLE c INHERIT p,INHERIT denied", true, fixture.Diagnostic{Code: "42804", Message: `child table is missing column "a"`}},
		{"DropBeforeMissing", "a", "ALTER TABLE c ADD spare INT,DROP absent,INHERIT missing_parent", false, fixture.Diagnostic{Code: "42703", Message: `column "absent" of relation "c" does not exist`}},
		{"CheckBindingBeforeMissing", "a", "ALTER TABLE c ADD spare INT,ADD CONSTRAINT invalid CHECK(missing>0),INHERIT missing_parent", false, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				s.exec("CREATE TABLE denied(a INT)", 0)
				s.exec("CREATE TABLE c("+tc.child+" INT)", 0)
				s.exec("INSERT INTO c VALUES(4)", 1)
				if tc.denied {
					s.exec("GRANT USAGE ON SCHEMA public TO maint_order_owner", 0)
					s.exec("ALTER TABLE p OWNER TO maint_order_owner", 0)
					s.exec("ALTER TABLE c OWNER TO maint_order_owner", 0)
					s.exec("SET ROLE maint_order_owner", 0)
				}
				s.fail(tc.query, tc.diagnostic)
				if tc.denied {
					s.exec("RESET ROLE", 0)
				}
				s.edges("c")
				inheritanceAlterAttributes(s, "c", [][]any{{tc.child, int64(1), true, int64(0)}})
				inheritanceAlterFacts(s, "c", nil)
				s.values("SELECT * FROM c", []string{tc.child}, []string{"INT4"}, [][]any{{int64(4)}})
				// The isolated scenario has no other holder of these relations;
				// observing all participants also detects leaked owner grants.
				s.values("SELECT count(*) AS n FROM pg_locks WHERE locktype='relation' AND relation IN('p'::regclass::oid,'c'::regclass::oid,'denied'::regclass::oid) AND mode IN('AccessExclusiveLock','ShareUpdateExclusiveLock')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				s.exec("ALTER TABLE c ADD recovered INT", 0)
				s.values("SELECT "+tc.child+",recovered FROM c", []string{tc.child, "recovered"}, []string{"INT4", "INT4"}, [][]any{{int64(4), nil}})
			}, "maint_order_owner")
		})
	}
}

func TestReferenceInheritanceMaintReviewDetachKinds(t *testing.T) {
	cases := []struct {
		name       string
		setup      []string
		diagnostic fixture.Diagnostic
		parents    [][]any
	}{
		{"Typed", []string{"CREATE TYPE maint_detach_shape AS(a INT)", "CREATE TABLE c OF maint_detach_shape"}, fixture.Diagnostic{Code: "42P01", Message: `relation "p" is not a parent of relation "c"`}, nil},
		{"Partitioned", []string{"CREATE TABLE c(a INT) PARTITION BY RANGE(a)"}, fixture.Diagnostic{Code: "42P01", Message: `relation "p" is not a parent of relation "c"`}, nil},
		{"Partition", []string{"CREATE TABLE r(a INT) PARTITION BY RANGE(a)", "CREATE TABLE c PARTITION OF r FOR VALUES FROM(0) TO(10)"}, fixture.Diagnostic{Code: "42809", Message: "cannot change inheritance of a partition"}, [][]any{{"r", int64(1), false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				for _, q := range tc.setup {
					s.exec(q, 0)
				}
				s.fail("ALTER TABLE c NO INHERIT p", tc.diagnostic)
				if tc.name == "Partitioned" {
					diagnostic := tc.diagnostic
					if !s.b.PG {
						// Correct detach diagnostics do not admit mixed physical
						// partition ALTER. PostgreSQL reaches its nonparent error.
						diagnostic = fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"}
					}
					s.fail("ALTER TABLE c NO INHERIT p,ADD extra INT", diagnostic)
					s.values("SELECT attname FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"a"}})
				}
				inheritanceMaintOrdinals(s, "c", tc.parents)
				s.values("SELECT a FROM ONLY c", []string{"a"}, []string{"INT4"}, nil)
				s.values("SELECT count(*) AS n FROM p", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
}
