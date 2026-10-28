package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayAggCarrierDiagnostic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			// The default supplies the existing public PG_NODE_TREE carrier;
			// this is an overload diagnostic, not an incidental defaults test.
			run(fixture.Exec("CREATE TABLE aa_carrier(v INT DEFAULT 1)", 0))
			run(fixture.Exec("CREATE VIEW aa_carrier_view AS SELECT d.adbin FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='aa_carrier'", 0))
			// REL_18_0 parse_coerce.c, enforce_generic_type_consistency's
			// ANYARRAY result branch uses ERRCODE_UNDEFINED_OBJECT (42704),
			// not DATATYPE_MISMATCH (42804), when get_array_type is absent.
			// Pinned PostgreSQL 18.0 confirms absent Position/optional fields.
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					s := fixture.ImplicitError("SELECT array_agg(adbin) FROM aa_carrier_view", fixture.Diagnostic{Code: "42704", Message: "could not find array type for data type pg_node_tree"})
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				})
			}
		})
	}
}
