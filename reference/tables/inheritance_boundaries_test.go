package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		selectRows  bool
		affected    int64
		rows        [][]any
	}{
		{"scan", "SELECT a FROM p ORDER BY a", true, 0, [][]any{{int64(1)}, {int64(2)}}},
		{"empty_scan", "SELECT a FROM p WHERE false", true, 0, nil},
		{"nested_scan", "SELECT a FROM (SELECT a FROM p) sub ORDER BY a", true, 0, [][]any{{int64(1)}, {int64(2)}}},
		{"view_source", "CREATE VIEW v AS SELECT a FROM p", false, 0, nil},
		{"insert_select", "INSERT INTO sink SELECT a FROM p", false, 2, nil},
		{"update_parent", "UPDATE p SET a=a+10", false, 2, nil},
		{"empty_update", "UPDATE p SET a=99 WHERE false", false, 0, nil},
		{"delete_parent", "DELETE FROM p", false, 2, nil},
		{"empty_delete", "DELETE FROM p WHERE false", false, 0, nil},
		{"update_from", "UPDATE sink SET a=p.a+10 FROM p WHERE sink.a=p.a", false, 1, nil},
		{"delete_using", "DELETE FROM sink USING p WHERE sink.a=p.a", false, 1, nil},
		{"merge_source", "MERGE INTO sink d USING (SELECT a FROM p) s ON d.a=s.a WHEN NOT MATCHED THEN INSERT(a) VALUES(s.a)", false, 0, nil},
		{"direct_merge_source", "MERGE INTO sink d USING p s ON d.a=s.a WHEN NOT MATCHED THEN INSERT(a) VALUES(s.a)", false, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				s.exec("INSERT INTO p VALUES(1)", 1)
				s.exec("INSERT INTO c VALUES(2)", 1)
				s.exec("CREATE TABLE sink(a INT)", 0)
				s.exec("INSERT INTO sink VALUES(1)", 1)
				if s.b.PG {
					if tc.selectRows {
						s.values(tc.query, []string{"a"}, []string{"INT4"}, tc.rows)
					} else {
						s.exec(tc.query, tc.affected)
					}
					if tc.name == "update_parent" {
						s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(12)}})
					}
					if tc.name == "delete_parent" {
						s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, nil)
					}
					if tc.name == "merge_source" {
						s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					}
					// Pinned PostgreSQL through pq reports RowsAffected0 for MERGE;
					// actual target rows independently prove descendant-source execution.
					if tc.name == "direct_merge_source" {
						s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					}
				} else {
					step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "recursive table inheritance is unsupported"})
					step.SelectRows = tc.selectRows
					s.run(step)
					s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
					s.values("SELECT * FROM sink", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
					if tc.name == "view_source" {
						s.absent("v")
					}
				}
			})
		})
	}
}

func TestReferenceInheritanceHeldPreparedBoundary(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("CREATE VIEW v AS SELECT a FROM p", 0)
		stmt, err := s.c.PrepareContext(s.p.Ctx, "SELECT a FROM p ORDER BY a")
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(2)", 1)
		rows, err := stmt.QueryContext(s.p.Ctx)
		if s.b.PG {
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				s.t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(s.t, true, columns, []string{"a"}, []string{"INT4"}, []bool{true})
			fixture.TransactionRowValues(s.t, rows, 1, 2)
			s.values("SELECT * FROM v ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		} else {
			if rows != nil {
				rows.Close()
			}
			d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "recursive table inheritance is unsupported"}
			fixture.AssertDiagnostic(s.t, err, &d)
			step := fixture.ImplicitError("SELECT * FROM v", fixture.Diagnostic{Code: "0A000", Message: "recursive table inheritance is unsupported"})
			step.SelectRows = true
			s.run(step)
		}
	})
}

func TestReferenceInheritanceAlterBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"parent_default", "ALTER TABLE p ALTER a SET DEFAULT 8"},
		{"parent_drop_default", "ALTER TABLE p ALTER a DROP DEFAULT"},
		{"child_default", "ALTER TABLE c ALTER a SET DEFAULT 8"},
		{"parent_nn", "ALTER TABLE p ALTER a SET NOT NULL"},
		{"child_nn", "ALTER TABLE c ALTER a SET NOT NULL"},
		{"parent_nullable", "ALTER TABLE p ALTER a DROP NOT NULL"},
		{"parent_rename", "ALTER TABLE p RENAME COLUMN a TO renamed"},
		{"parent_type", "ALTER TABLE p ALTER a TYPE BIGINT"},
		{"parent_add", "ALTER TABLE p ADD COLUMN b TEXT"},
		{"child_local_add", "ALTER TABLE c ADD COLUMN b TEXT"},
		{"parent_only", "ALTER TABLE ONLY p ALTER a SET DEFAULT 8"},
		{"action_list", "ALTER TABLE p ADD COLUMN b INT,ALTER a SET NOT NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT DEFAULT 7)", 0)
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				if s.b.PG {
					s.exec(tc.query, 0)
					s.exec("INSERT INTO c DEFAULT VALUES", 1)
					switch tc.name {
					case "parent_default", "child_default":
						s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}})
					case "parent_drop_default":
						s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{nil}})
					case "parent_rename":
						s.values("SELECT renamed FROM c", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(7)}})
					case "parent_type":
						s.values("SELECT a FROM c", []string{"a"}, []string{"INT8"}, [][]any{{int64(7)}})
					case "parent_add", "child_local_add":
						s.values("SELECT * FROM c", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}})
					case "action_list":
						s.values("SELECT * FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}})
					default:
						s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
					}
				} else {
					s.fail(tc.query, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE inheritance operations are unsupported"})
					s.exec("INSERT INTO c DEFAULT VALUES", 1)
					s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
				}
			})
		})
	}
}

func TestReferenceInheritanceInheritedColumnProtection(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		for _, tc := range []struct{ query, message, position string }{
			{"ALTER TABLE c RENAME COLUMN a TO b", `cannot rename inherited column "a"`, ""},
			{"ALTER TABLE c DROP COLUMN a", `cannot drop inherited column "a"`, ""},
			{"ALTER TABLE c ALTER a TYPE BIGINT", `cannot alter inherited column "a"`, "21"},
		} {
			s.fail(tc.query, fixture.Diagnostic{Code: "42P16", Message: tc.message, Position: tc.position})
		}
		s.exec("INSERT INTO c VALUES(7)", 1)
		s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func TestReferenceInheritanceLockBoundary(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("BEGIN", 0)
		if s.b.PG {
			s.exec("LOCK TABLE p IN ACCESS SHARE MODE", 0)
		} else {
			s.fail("LOCK TABLE p IN ACCESS SHARE MODE", fixture.Diagnostic{Code: "0A000", Message: "recursive table inheritance is unsupported"})
		}
		s.exec("ROLLBACK", 0)
		s.exec("BEGIN", 0)
		s.exec("LOCK TABLE ONLY p IN ACCESS SHARE MODE", 0)
		s.exec("COMMIT", 0)
	})
}

func TestReferenceInheritanceDependencyShapeBoundary(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("DROP SCHEMA IF EXISTS inheritance_types CASCADE", 0)
		s.exec("CREATE SCHEMA inheritance_types", 0)
		s.exec("CREATE DOMAIN inheritance_types.d AS INT", 0)
		s.exec("CREATE TABLE p(a inheritance_types.d,keep INT DEFAULT 7 NOT NULL,CONSTRAINT keep_positive CHECK(keep>0))", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1,10)", 1)
		s.exec("INSERT INTO c VALUES(2,20)", 1)
		s.values("SELECT * FROM c", []string{"a", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}})
		s.values("SELECT * FROM c WHERE false", []string{"a", "keep"}, []string{"INT4", "INT4"}, nil)
		s.edges("c", "p")
		if s.b.PG {
			// PostgreSQL removes domain-dependent columns, not their tables.
			// The surviving child retains its independent rows and keep constraints.
			s.exec("DROP SCHEMA inheritance_types CASCADE", 0)
			s.values("SELECT * FROM ONLY p", []string{"keep"}, []string{"INT4"}, [][]any{{int64(10)}})
			s.values("SELECT * FROM c", []string{"keep"}, []string{"INT4"}, [][]any{{int64(20)}})
			s.values("SELECT * FROM c WHERE false", []string{"keep"}, []string{"INT4"}, nil)
			s.exec("SET search_path=inheritance_types,public", 0)
			s.values("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"public"}})
			s.exec("SET search_path=public", 0)
			s.exec("INSERT INTO c DEFAULT VALUES", 1)
			s.fail("INSERT INTO c VALUES(30),(-1)", inheritanceCheckDiagnostic("c", "keep_positive", "-1"))
			s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "keep", "null"))
			s.values("SELECT * FROM c ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(20)}})
		} else {
			s.fail("DROP SCHEMA inheritance_types CASCADE", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE inheritance operations are unsupported"})
			// Rejection is atomic, including the provider schema/domain and both
			// physical row shapes. Subsequent public consumers remain usable.
			s.values("SELECT * FROM c", []string{"a", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}})
			s.values("SELECT * FROM c WHERE false", []string{"a", "keep"}, []string{"INT4", "INT4"}, nil)
			s.exec("SET search_path=inheritance_types,public", 0)
			s.values("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"inheritance_types"}})
			s.exec("SET search_path=public", 0)
			s.exec("CREATE TABLE domain_probe(v inheritance_types.d)", 0)
			s.exec("INSERT INTO domain_probe VALUES(5)", 1)
			s.values("SELECT * FROM domain_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
			s.exec("INSERT INTO c(a) VALUES(3)", 1)
			s.fail("INSERT INTO c VALUES(4,30),(5,-1)", inheritanceCheckDiagnostic("c", "keep_positive", "5, -1"))
			s.fail("INSERT INTO c VALUES(6,NULL)", inheritanceNullDiagnostic("c", "keep", "6, null"))
			s.values("SELECT * FROM c ORDER BY keep", []string{"a", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(7)}, {int64(2), int64(20)}})
		}
		s.constraints("c", [][]any{{"keep_positive", "c"}, {"p_keep_not_null", "n"}})
		s.edges("c", "p")
		s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "c"))
		s.exec("DROP TABLE c", 0)
		if s.b.PG {
			s.values("SELECT * FROM p", []string{"keep"}, []string{"INT4"}, [][]any{{int64(10)}})
		} else {
			// Reading the now-standalone parent proves its original physical
			// values and metadata survived, without requesting recursive scans.
			s.values("SELECT * FROM p", []string{"a", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}})
			s.constraints("p", [][]any{{"keep_positive", "c"}, {"p_keep_not_null", "n"}})
			s.exec("DROP TABLE domain_probe", 0)
		}
		s.exec("DROP TABLE p", 0)
		s.exec("DROP SCHEMA IF EXISTS inheritance_types CASCADE", 0)
	})
}

func TestReferenceInheritanceDependencyDefinitionBoundary(t *testing.T) {
	for _, definition := range []string{"default", "check"} {
		t.Run(definition, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("DROP SCHEMA IF EXISTS inheritance_types CASCADE", 0)
				s.exec("CREATE SCHEMA inheritance_types", 0)
				s.exec("CREATE DOMAIN inheritance_types.d AS INT", 0)
				if definition == "default" {
					s.exec("CREATE TABLE p(keep INT DEFAULT 9::inheritance_types.d)", 0)
				} else {
					s.exec("CREATE TABLE p(keep INT,CONSTRAINT keep_positive CHECK(keep>0::inheritance_types.d))", 0)
				}
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				s.exec("INSERT INTO p VALUES(10)", 1)
				s.exec("INSERT INTO c VALUES(20)", 1)
				s.values("SELECT * FROM c", []string{"keep"}, []string{"INT4"}, [][]any{{int64(20)}})
				s.edges("c", "p")
				if s.b.PG {
					// Provider removal drops only the dependent definition; row
					// shape, stored values and inheritance edges survive.
					s.exec("DROP SCHEMA inheritance_types CASCADE", 0)
					s.values("SELECT * FROM ONLY p", []string{"keep"}, []string{"INT4"}, [][]any{{int64(10)}})
				} else {
					s.fail("DROP SCHEMA inheritance_types CASCADE", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE inheritance operations are unsupported"})
					s.exec("SET search_path=inheritance_types,public", 0)
					s.values("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"inheritance_types"}})
					s.exec("SET search_path=public", 0)
					s.exec("CREATE TABLE domain_probe(v inheritance_types.d)", 0)
					s.exec("INSERT INTO domain_probe VALUES(5)", 1)
					s.values("SELECT * FROM domain_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
				}
				s.values("SELECT * FROM c", []string{"keep"}, []string{"INT4"}, [][]any{{int64(20)}})
				s.values("SELECT * FROM c WHERE false", []string{"keep"}, []string{"INT4"}, nil)
				if definition == "default" {
					s.exec("INSERT INTO c DEFAULT VALUES", 1)
					want := any(int64(9))
					if s.b.PG {
						want = nil
					}
					s.values("SELECT * FROM c ORDER BY keep NULLS FIRST", []string{"keep"}, []string{"INT4"}, [][]any{{want}, {int64(20)}})
					s.constraints("c", nil)
				} else if s.b.PG {
					s.exec("INSERT INTO c VALUES(30),(-1)", 2)
					s.values("SELECT * FROM c ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(20)}, {int64(30)}})
					s.constraints("c", nil)
				} else {
					s.fail("INSERT INTO c VALUES(30),(-1)", inheritanceCheckDiagnostic("c", "keep_positive", "-1"))
					s.values("SELECT * FROM c", []string{"keep"}, []string{"INT4"}, [][]any{{int64(20)}})
					s.exec("INSERT INTO c VALUES(30)", 1)
					s.constraints("c", [][]any{{"keep_positive", "c"}})
				}
				s.edges("c", "p")
				s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "c"))
				s.exec("DROP TABLE c", 0)
				s.values("SELECT * FROM p", []string{"keep"}, []string{"INT4"}, [][]any{{int64(10)}})
				s.exec("DROP TABLE p", 0)
				if !s.b.PG {
					s.exec("DROP TABLE domain_probe", 0)
				}
				s.exec("DROP SCHEMA IF EXISTS inheritance_types CASCADE", 0)
			})
		})
	}
}

func TestReferenceInheritanceRetirementAuthority(t *testing.T) {
	for _, retirement := range []string{"removed_child", "removed_parent"} {
		t.Run(retirement, func(t *testing.T) {
			for _, definition := range []string{"column", "default", "check"} {
				t.Run(definition, func(t *testing.T) {
					inheritanceScenario(t, func(s *inheritanceCase) {
						const schema = "inheritance_retirement"
						s.exec("DROP SCHEMA IF EXISTS "+schema+" CASCADE", 0)
						s.exec("CREATE SCHEMA "+schema, 0)
						s.exec("CREATE DOMAIN "+schema+".d AS INT", 0)
						s.exec("CREATE SCHEMA inheritance_retained", 0)
						s.exec("CREATE TABLE inheritance_retained.witness(v INT DEFAULT 42)", 0)
						s.exec("INSERT INTO inheritance_retained.witness DEFAULT VALUES", 1)
						parent, child := "public.retirement_p", schema+".retirement_c"
						if retirement == "removed_parent" {
							parent, child = schema+".retirement_p", "public.retirement_c"
						}
						columns := "keep INT DEFAULT 7 NOT NULL,CONSTRAINT keep_positive CHECK(keep>0)"
						switch definition {
						case "column":
							columns = "a " + schema + ".d," + columns
						case "default":
							columns = "keep INT DEFAULT 9::" + schema + ".d,CONSTRAINT keep_positive CHECK(keep>0)"
						case "check":
							columns = "keep INT DEFAULT 7 NOT NULL,CONSTRAINT keep_positive CHECK(keep>0::" + schema + ".d)"
						}
						s.exec("CREATE TABLE "+parent+"("+columns+")", 0)
						s.exec("CREATE TABLE "+child+"() INHERITS("+parent+")", 0)
						names, types := []string{"keep"}, []string{"INT4"}
						parentValues, childValues := "10", "20"
						childRows := [][]any{{int64(20)}}
						if definition == "column" {
							names, types = []string{"a", "keep"}, []string{"INT4", "INT4"}
							parentValues, childValues = "1,10", "2,20"
							childRows = [][]any{{int64(2), int64(20)}}
						}
						s.exec("INSERT INTO "+parent+" VALUES("+parentValues+")", 1)
						s.exec("INSERT INTO "+child+" VALUES("+childValues+")", 1)
						s.values("SELECT * FROM "+child, names, types, childRows)
						s.values("SELECT * FROM "+child+" WHERE false", names, types, nil)
						s.edges("retirement_c", "retirement_p")
						var held *sql.Rows
						if !s.b.PG {
							var err error
							held, err = s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), "SELECT * FROM "+child)
							if err != nil {
								s.t.Fatal(err)
							}
							defer held.Close()
						}
						s.exec("DROP SCHEMA "+schema+" CASCADE", 0)
						if !s.b.PG {
							fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.c, Rows: held, Query: "SELECT * FROM " + child}, s.record, fixture.Select("SELECT * FROM "+child, names, types, childRows))
						}
						s.exec("SET search_path="+schema+",inheritance_retained,public", 0)
						s.values("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"inheritance_retained"}})
						s.exec("SET search_path=public", 0)
						s.values("SELECT * FROM inheritance_retained.witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
						s.absent("retirement_c")
						s.edges("retirement_c")
						if retirement == "removed_parent" {
							s.absent("retirement_p")
							s.edges("retirement_p")
							s.values("SELECT c.conname FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relname='retirement_c'", []string{"conname"}, []string{"NAME"}, nil)
						} else {
							s.values("SELECT * FROM "+parent, []string{"keep"}, []string{"INT4"}, [][]any{{int64(10)}})
							s.values("SELECT * FROM "+parent+" WHERE false", []string{"keep"}, []string{"INT4"}, nil)
							s.edges("retirement_p")
							if definition == "default" {
								s.exec("INSERT INTO "+parent+" DEFAULT VALUES", 1)
								s.values("SELECT * FROM "+parent+" ORDER BY keep NULLS FIRST", []string{"keep"}, []string{"INT4"}, [][]any{{nil}, {int64(10)}})
								s.constraints("retirement_p", [][]any{{"keep_positive", "c"}})
							} else if definition == "check" {
								s.exec("INSERT INTO "+parent+" VALUES(30),(-1)", 2)
								s.exec("INSERT INTO "+parent+" DEFAULT VALUES", 1)
								s.values("SELECT * FROM "+parent+" ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(7)}, {int64(10)}, {int64(30)}})
								s.constraints("retirement_p", [][]any{{"retirement_p_keep_not_null", "n"}})
							} else {
								s.exec("INSERT INTO "+parent+" DEFAULT VALUES", 1)
								s.fail("INSERT INTO "+parent+" VALUES(30),(-1)", inheritanceCheckDiagnostic("retirement_p", "keep_positive", "-1"))
								s.fail("INSERT INTO "+parent+" VALUES(NULL)", inheritanceNullDiagnostic("retirement_p", "keep", "null"))
								s.values("SELECT * FROM "+parent+" ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(10)}})
								s.constraints("retirement_p", [][]any{{"keep_positive", "c"}, {"retirement_p_keep_not_null", "n"}})
							}
							s.exec("DROP TABLE "+parent+" RESTRICT", 0)
						}
						s.exec("DROP SCHEMA inheritance_retained CASCADE", 0)
					})
				})
			}
		})
	}
}
