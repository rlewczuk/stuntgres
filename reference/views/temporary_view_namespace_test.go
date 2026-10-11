package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewIsolation(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE VIEW isolation_same AS SELECT 7 AS v", nil)
		s.Utility("CREATE TEMP VIEW isolation_same AS SELECT 11 AS v", nil)
		fixture.TemporaryTableRows(s, s.Peer, "isolation_same", 7)
		s.Exec(s.Peer, "CREATE TEMPORARY VIEW isolation_same AS SELECT 22 AS v", 0)
		temporaryViewNotices(t, n)
		fixture.TemporaryTableRows(s, s.C, "isolation_same", 11)
		fixture.TemporaryTableRows(s, s.C, "pg_temp.isolation_same", 11)
		fixture.TemporaryTableRows(s, s.Peer, "isolation_same", 22)
		fixture.TemporaryTableRows(s, s.C, "public.isolation_same", 7)
		s.Utility("CREATE TEMP VIEW isolation_unique AS SELECT 33 AS v", nil)
		s.Missing(s.Peer, "isolation_unique", "v", "15")
		if s.Prepared {
			missing := fixture.ImplicitError("SELECT v FROM isolation_unique", fixture.Diagnostic{Code: "42P01", Message: `relation "isolation_unique" does not exist`, Position: "15"})
			missing.Prepared, missing.SelectRows, missing.ErrorStage = true, true, "prepare"
			fixture.RunStep(t, s.P.Ctx, s.Peer, s.Record, missing)
		}
		s.Exec(s.C, "SET search_path TO public,pg_temp", 0)
		fixture.TemporaryTableRows(s, s.C, "isolation_same", 7)
		s.Exec(s.C, "SET search_path TO pg_temp,public", 0)
		fixture.TemporaryTableRows(s, s.C, "isolation_same", 11)
	})
}

func TestReferenceTemporaryViewLifetime(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE lifetime_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO lifetime_public VALUES(7)", 1)
		s.Utility("CREATE TEMP TABLE lifetime_base(v INT) ON COMMIT PRESERVE ROWS", nil)
		s.Exec(s.C, "INSERT INTO lifetime_base VALUES(11)", 1)
		s.Utility("CREATE VIEW lifetime_v AS SELECT v FROM lifetime_base", nil)
		s.Utility("CREATE VIEW lifetime_w AS SELECT v FROM lifetime_v", nil)
		s.Utility("CREATE TEMP VIEW lifetime_explicit AS SELECT v FROM lifetime_public", nil)
		temporaryViewNotices(t, n, "lifetime_v", "lifetime_w")
		s.Exec(s.Peer, "CREATE TEMP VIEW lifetime_peer AS SELECT 22 AS v", 0)
		s.Utility("CREATE TEMP TABLE lifetime_delete(v INT) ON COMMIT DELETE ROWS", nil)
		s.Utility("CREATE VIEW lifetime_deleted AS SELECT v FROM lifetime_delete", nil)
		temporaryViewNotices(t, n, "lifetime_deleted")
		s.Exec(s.C, "INSERT INTO lifetime_delete VALUES(99)", 1)
		fixture.TemporaryTableRows(s, s.C, "lifetime_deleted")
		s.Exec(s.C, "BEGIN", 0)
		s.Exec(s.C, "INSERT INTO lifetime_delete VALUES(13)", 1)
		fixture.TemporaryTableRows(s, s.C, "lifetime_deleted", 13)
		s.Exec(s.C, "COMMIT", 0)
		fixture.TemporaryTableRows(s, s.C, "lifetime_deleted")
		s.Utility("ALTER TABLE lifetime_base RENAME TO lifetime_renamed", nil)
		s.Utility("CREATE TEMP TABLE lifetime_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO lifetime_base VALUES(99)", 1)
		fixture.TemporaryTableRows(s, s.C, "lifetime_w", 11)
		s.Utility("DROP TABLE lifetime_renamed CASCADE", nil)
		// CASCADE notices are separately pinned by the existing dependency scenarios.
		*n = nil
		s.Missing(s.C, "lifetime_w", "v", "15")
		s.Retire()
		fixture.TemporaryTableRows(s, s.Peer, "lifetime_public", 7)
		fixture.TemporaryTableRows(s, s.Peer, "lifetime_peer", 22)
		temporaryViewSelect(s, s.Peer, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='lifetime_explicit' OR viewname='lifetime_deleted'", []string{"viewname"}, []string{"NAME"}, [][]any{})
	})
}

func TestReferenceTemporaryViewBindingSecurity(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP TABLE binding_base(v INT,txt TEXT)", nil)
		s.Exec(s.C, "INSERT INTO binding_base VALUES(11,'a')", 1)
		s.Utility("CREATE VIEW binding_v AS SELECT * FROM binding_base", nil)
		temporaryViewNotices(t, n, "binding_v")
		s.Utility("ALTER TABLE binding_base ADD COLUMN extra INT", nil)
		temporaryViewSelect(s, s.C, "SELECT * FROM binding_v", []string{"v", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "a"}})
		s.Utility("CREATE OR REPLACE VIEW binding_v AS SELECT v,txt,extra FROM binding_base", nil)
		temporaryViewNotices(t, n, "binding_v")
		s.Utility("ALTER VIEW binding_v RENAME TO binding_renamed", nil)
		s.Utility("ALTER VIEW binding_renamed RENAME COLUMN v TO k", nil)
		temporaryViewSelect(s, s.C, "SELECT k,txt,extra FROM binding_renamed", []string{"k", "txt", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(11), "a", nil}})
		s.Utility("DROP VIEW binding_renamed", nil)
		s.Missing(s.C, "binding_renamed", "k", "15")
	})
}

func TestReferenceTemporaryViewDiagnostics(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE diagnostic_source(id INT,txt TEXT)", nil)
		s.Exec(s.C, "INSERT INTO diagnostic_source VALUES(7,'safe')", 1)
		s.Utility("CREATE TEMP TABLE diagnostic_temp(id INT)", nil)
		cases := []struct{ q, code, message, position, notice string }{
			{"CREATE TEMP VIEW public.bad AS SELECT id FROM diagnostic_source", "42P16", "cannot create temporary relation in non-temporary schema", "", ""},
			{"CREATE VIEW public.bad AS SELECT id FROM diagnostic_temp", "42P16", "cannot create temporary relation in non-temporary schema", "", "bad"},
			{"CREATE TEMP VIEW absent_schema.bad AS SELECT id FROM diagnostic_source", "3F000", `schema "absent_schema" does not exist`, "", ""},
			{"CREATE TEMP VIEW bad AS SELECT $1 FROM diagnostic_source", "42P02", "there is no parameter $1", "32", ""},
			{"CREATE TEMP VIEW bad(x,y,z) AS SELECT id,txt FROM diagnostic_source", "42601", "CREATE VIEW specifies more column names than columns", "", ""},
			{"CREATE TEMP VIEW bad AS SELECT id,id FROM diagnostic_source", "42701", `column "id" specified more than once`, "", ""},
			{`CREATE TEMP VIEW "ébad" AS SELECT missing FROM diagnostic_source`, "42703", `column "missing" does not exist`, "35", ""},
			{"CREATE TEMP VIEW bad AS SELECT id FROM absent", "42P01", `relation "absent" does not exist`, "40", ""},
			{"CREATE TEMP VIEW bad AS SELECT id FROM diagnostic_source WHERE id", "42804", "argument of WHERE must be type boolean, not type integer", "64", ""},
			{"CREATE TEMP VIEW bad WITH(security_barrier=wat) AS SELECT id FROM diagnostic_source", "22023", `invalid value for boolean option "security_barrier": wat`, "", ""},
			{"CREATE TEMP VIEW bad WITH(unknown_option=true) AS SELECT id FROM diagnostic_source", "22023", `unrecognized parameter "unknown_option"`, "", ""},
			{"CREATE TEMP VIEW bad WITH(security_invoker=true,security_invoker=false) AS SELECT id FROM diagnostic_source", "22023", `parameter "security_invoker" specified more than once`, "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.q, func(t *testing.T) {
				current := *s
				current.T = t
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				step.Prepared = s.Prepared
				step.ErrorStage = "execute"
				fixture.RunStep(t, s.P.Ctx, s.C, s.Record, step)
				if tc.notice == "" {
					temporaryViewNotices(t, n)
				} else {
					temporaryViewNotices(t, n, tc.notice)
				}
				temporaryViewSelect(&current, s.C, "SELECT id,txt FROM diagnostic_source", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "safe"}})
				temporaryViewSelect(&current, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='bad' OR viewname='ébad'", []string{"viewname"}, []string{"NAME"}, [][]any{})
			})
		}
	})
}
