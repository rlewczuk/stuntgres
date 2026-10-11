package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewNamespaceReplacement(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE VIEW replacement_same AS SELECT 7 AS v", nil)
		s.Utility("CREATE TEMP TABLE replacement_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO replacement_base VALUES(11)", 1)
		s.Utility("CREATE OR REPLACE VIEW replacement_same AS SELECT v FROM replacement_base", nil)
		temporaryViewNotices(t, n, "replacement_same")
		fixture.TemporaryTableRows(s, s.C, "replacement_same", 11)
		fixture.TemporaryTableRows(s, s.C, "public.replacement_same", 7)
		s.Utility("CREATE VIEW replacement_marker AS SELECT v FROM replacement_base", nil)
		temporaryViewNotices(t, n, "replacement_marker")
		schema := temporaryViewSchema(s, "replacement_marker")
		s.Utility("CREATE TEMP VIEW "+schema+".replacement_actual AS SELECT 13 AS v", nil)
		temporaryViewNotices(t, n)
		fixture.TemporaryTableRows(s, s.C, "replacement_actual", 13)
		s.Exec(s.C, "SET search_path TO pg_temp,public", 0)
		s.Utility("CREATE VIEW replacement_path AS SELECT 17 AS v", nil)
		temporaryViewNotices(t, n)
		if actual := temporaryViewSchema(s, "replacement_path"); actual != schema {
			t.Fatal(actual, schema)
		}
		s.Exec(s.Peer, "CREATE TEMP VIEW replacement_peer AS SELECT 22 AS v", 0)
		var peerSchema string
		if err := s.Peer.QueryRowContext(s.P.Ctx, "SELECT schemaname FROM pg_catalog.pg_views WHERE viewname='replacement_peer'").Scan(&peerSchema); err != nil {
			t.Fatal(err)
		}
		s.Utility("CREATE TEMP VIEW "+peerSchema+".replacement_forbidden AS SELECT 1 AS v", fixture.TempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", ""))
		temporaryViewNotices(t, n)
	})
}

func TestReferenceTemporaryViewSequenceInferenceBoundary(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP SEQUENCE expression_temp START 41", nil)
		s.Utility("CREATE TABLE expression_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO expression_public VALUES(7)", 1)
		q := "CREATE VIEW expression_only AS SELECT nextval('expression_temp') AS n FROM expression_public"
		if s.Postgres {
			s.Utility(q, nil)
			temporaryViewNotices(t, n)
			if schema := temporaryViewSchema(s, "expression_only"); schema != "public" {
				t.Fatal("expression-only dependency incorrectly inferred TEMP", schema)
			}
		} else {
			s.Utility(q, fixture.TempReferenceDiagnostic("0A000", "temporary views are unsupported", "", ""))
			temporaryViewNotices(t, n)
		}
		temporaryViewSelect(s, s.C, "SELECT last_value,is_called FROM expression_temp", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}})
		s.Utility("CREATE TEMP VIEW expression_explicit AS SELECT nextval('expression_temp') AS n FROM expression_public", nil)
		temporaryViewNotices(t, n)
		temporaryViewSelect(s, s.C, "SELECT last_value,is_called FROM expression_temp", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}})
		temporaryViewSelect(s, s.C, "SELECT n FROM expression_explicit", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}})
	})
}

func TestReferenceTemporaryViewDependencies(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP TABLE dependency_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO dependency_base VALUES(11)", 1)
		s.Utility("CREATE VIEW dependency_v AS SELECT v FROM dependency_base", nil)
		s.Utility("CREATE VIEW dependency_w AS SELECT v FROM dependency_v", nil)
		temporaryViewNotices(t, n, "dependency_v", "dependency_w")
		d := fixture.TempReferenceDiagnostic("2BP01", "cannot drop table dependency_base because other objects depend on it", "view dependency_v depends on table dependency_base\nview dependency_w depends on view dependency_v", "")
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.Utility("DROP TABLE dependency_base RESTRICT", d)
		fixture.TemporaryTableRows(s, s.C, "dependency_w", 11)
		s.Utility("DROP TABLE dependency_base CASCADE", nil)
		fixture.DomainReferenceNotice(t, n, "drop cascades to 2 other objects", "drop cascades to view dependency_v\ndrop cascades to view dependency_w")
		s.Missing(s.C, "dependency_v", "v", "15")
		s.Missing(s.C, "dependency_w", "v", "15")
	})
}
