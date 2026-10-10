package reference_test

import "testing"

func TestReferenceQuerySetOpsViewContracts(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsSeed(t, p, b)
		for _, s := range []referenceStep{
			referenceExec("CREATE VIEW sv AS SELECT v FROM sl UNION ALL SELECT v FROM sr", 0),
			referenceExec("CREATE VIEW set_nested(override_label) AS (SELECT v FROM sl ORDER BY v LIMIT 2) UNION ALL (VALUES(9) EXCEPT SELECT 8)", 0),
			referenceExec("CREATE DOMAIN set_domain AS INT", 0),
			referenceExec("CREATE VIEW set_domain_view AS SELECT 1::set_domain AS d UNION SELECT NULL", 0),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT * FROM set_nested", "override_label", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(9)}}))
		querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT * FROM set_domain_view", "d", "INT4", [][]any{{int64(1)}, {nil}}))
		// Pinned18.0 artifact8 observes these exact displays. Width40 is
		// an exact contract; the separate width20 consumer remains replay-only.
		nested := "( SELECT sl.v AS override_label\n   FROM sl\n  ORDER BY sl.v\n LIMIT 2)\nUNION ALL (\n         SELECT \"*VALUES*\".column1 AS override_label\n           FROM (VALUES (9)) \"*VALUES*\"\n        EXCEPT\n         SELECT 8 AS override_label\n);"
		for _, query := range []string{
			"SELECT pg_get_viewdef('set_nested')",
			"SELECT pg_get_viewdef('set_nested',true)",
			"SELECT pg_get_viewdef('set_nested'::REGCLASS,40)",
		} {
			querySetOpsRoutes(t, p, b, referenceSelect(query, []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{nested}}))
		}
		domain := " SELECT (1)::set_domain AS d\nUNION\n SELECT NULL::integer AS d;"
		querySetOpsRoutes(t, p, b, referenceSelect("SELECT pg_get_viewdef('set_domain_view')", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{domain}}))
		// Pinned18.0 treats a stored set view as an opaque lock-lineage
		// boundary: outer FOR UPDATE succeeds; inline derived sets reject.
		all := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}, {nil}}
		querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT * FROM sv FOR UPDATE", "v", "INT4", all))
		stmt, err := b.db.PrepareContext(p.ctx, "SELECT * FROM sv")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		s := querySetOpsBag("SELECT * FROM sv", "v", "INT4", all)
		referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, s.query}, b.recorder, s)
		for _, step := range []referenceStep{
			referenceExec("ALTER TABLE sl RENAME COLUMN v TO renamed_v", 0),
			referenceExec("ALTER TABLE sl RENAME TO renamed_sl", 0),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		}
		querySetOpsRoutes(t, p, b, s)
		referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, s.query}, b.recorder, s)
		// Current-name reconstruction must preserve the original left view label.
		def := " SELECT renamed_sl.renamed_v AS v\n   FROM renamed_sl\nUNION ALL\n SELECT sr.v\n   FROM sr;"
		querySetOpsRoutes(t, p, b, referenceSelect("SELECT pg_get_viewdef('sv')", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{def}}))
	})
}
