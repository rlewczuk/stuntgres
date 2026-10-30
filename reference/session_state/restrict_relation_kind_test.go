package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const rkSetting = "restrict_nonsystem_relation_kind"

func rkSeed(s *physicalCase) {
	s.run(fixture.Exec("CREATE TABLE rk_t(id integer)", 0))
	s.run(fixture.Exec("INSERT INTO rk_t VALUES(1),(2)", 2))
	s.run(fixture.Exec("CREATE VIEW rk_v AS SELECT id FROM rk_t", 0))
	s.run(fixture.Exec("CREATE VIEW rk_nested AS SELECT id FROM rk_v", 0))
}

func rkRows(name string, ids ...int64) fixture.Step {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return fixture.Select("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

func rkSet(s *physicalCase, value string) {
	s.run(fixture.Exec("SET "+rkSetting+"="+physicalLiteral(value), 0))
}

func rkDenied(query, name string, prepared, rows bool) fixture.Step {
	step := physicalError(query, physicalDiagnostic("55000", `access to non-system view "`+name+`" is restricted`), prepared, rows)
	if prepared {
		step.ErrorStage = "prepare"
	}
	return step
}

func rkRead(s *physicalCase, restricted bool) {
	if restricted {
		s.run(rkDenied("SELECT id FROM rk_v ORDER BY id", "rk_v", false, true))
	} else {
		s.run(rkRows("rk_v", 1, 2))
	}
}

func TestReferenceRestrictRelationKindViews(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		rkSeed(s)
		for _, name := range []string{"rk_v", "rk_nested"} {
			for _, prepared := range []bool{false, true} {
				step := rkRows(name, 1, 2)
				step.Prepared = prepared
				s.run(step)
				step = fixture.Exec("INSERT INTO "+name+" VALUES(3)", 1)
				step.Prepared = prepared
				s.run(step)
				s.run(rkRows("rk_t", 1, 2, 3))
				step = fixture.Exec("UPDATE "+name+" SET id=4 WHERE id=3", 1)
				step.Prepared = prepared
				s.run(step)
				s.run(rkRows("rk_t", 1, 2, 4))
				step = fixture.Exec("DELETE FROM "+name+" WHERE id=4", 1)
				step.Prepared = prepared
				s.run(step)
				s.run(rkRows("rk_t", 1, 2))
			}
		}
		for _, value := range []string{"view", "VIEW", `"VIEW"`, "view,view", "foreign-table,view"} {
			rkSet(s, value)
			s.run(clientText("SHOW "+rkSetting, rkSetting, value))
			for _, name := range []string{"rk_v", "rk_nested"} {
				for _, prepared := range []bool{false, true} {
					for i, q := range []string{"SELECT id FROM " + name + " ORDER BY id", "INSERT INTO " + name + " VALUES(3)", "UPDATE " + name + " SET id=4 WHERE id=2", "DELETE FROM " + name + " WHERE id=2"} {
						s.run(rkDenied(q, name, prepared, i == 0))
						s.run(rkRows("rk_t", 1, 2))
					}
				}
			}
		}
		rkSet(s, "view")
		for _, tc := range []struct{ input, detail string }{
			{"table", `Unrecognized key word: "table".`}, {"all", `Unrecognized key word: "all".`},
			{"bogus,", "List syntax is invalid."}, {`""`, `Unrecognized key word: "".`},
			{`"a\b"`, `Unrecognized key word: "a\b".`},
		} {
			s.run(physicalError("SET "+rkSetting+"="+physicalLiteral(tc.input), clientInvalid(rkSetting, tc.input, tc.detail, ""), false, false))
			s.run(clientText("SHOW "+rkSetting, rkSetting, "view"))
			rkRead(s, true)
		}
		// Ordinary data operations and actual catalog providers remain usable.
		s.run(fixture.Exec("INSERT INTO rk_t VALUES(3)", 1))
		s.run(fixture.Exec("UPDATE rk_t SET id=4 WHERE id=3", 1))
		s.run(rkRows("rk_t", 1, 2, 4))
		s.run(fixture.Exec("DELETE FROM rk_t WHERE id=4", 1))
		s.run(fixture.Exec("TRUNCATE rk_t", 0))
		s.run(rkRows("rk_t"))
		s.run(fixture.Exec("INSERT INTO rk_t VALUES(1),(2)", 2))
		s.run(fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='public' AND viewname='rk_v'", []string{"viewname"}, []string{"NAME"}, [][]any{{"rk_v"}}))
		s.run(fixture.Select("SELECT relname FROM pg_class WHERE oid='rk_v'::regclass", []string{"relname"}, []string{"NAME"}, [][]any{{"rk_v"}}))
		s.run(clientText("SELECT setting FROM pg_settings WHERE name='"+rkSetting+"'", "setting", "view"))
		s.run(fixture.Exec("CREATE VIEW rk_catalog AS SELECT relname FROM pg_class", 0))
		s.run(rkDenied("SELECT relname FROM rk_catalog", "rk_catalog", false, true))
		for _, value := range []string{"foreign-table", "", "   "} {
			rkSet(s, value)
			s.run(rkRows("rk_v", 1, 2))
			s.run(rkRows("rk_nested", 1, 2))
		}
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		rkRead(s, false)
	})
}

func TestReferenceRestrictRelationKindPrivileges(t *testing.T) {
	physicalCases(t, []string{"rk_user"}, func(s *physicalCase) {
		rkSeed(s)
		s.run(fixture.Exec("CREATE ROLE rk_user", 0))
		s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rk_user", 0))
		s.run(fixture.Exec("SET ROLE rk_user", 0))
		rkSet(s, "view")
		rkRead(s, true)
		s.run(clientText("SELECT current_setting('"+rkSetting+"')", "current_setting", "view"))
		s.run(clientText("SELECT context FROM pg_settings WHERE name='"+rkSetting+"'", "context", "user"))
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		s.run(physicalError("SELECT id FROM rk_v", physicalDiagnostic("42501", "permission denied for view rk_v"), false, true))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("GRANT SELECT ON rk_v TO rk_user", 0))
		s.run(fixture.Exec("SET ROLE rk_user", 0))
		rkRead(s, false)
		s.run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceRestrictRelationKindPreparedLifetime(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		rkSeed(s)
		q := "SELECT id FROM rk_v ORDER BY id"
		held, err := s.conn.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		rkSet(s, "view")
		// PostgreSQL retains rewrite output. Native intentionally retains AST
		// and signatures, not an executable graph, and reanalyzes each use.
		want := rkRows("rk_v", 1, 2)
		if !s.pg {
			want = rkDenied(q, "rk_v", false, true)
		}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: q}, s.recorder, want)
		s.run(rkDenied(q, "rk_v", true, true))
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: q}, s.recorder, rkRows("rk_v", 1, 2))
	})
}
