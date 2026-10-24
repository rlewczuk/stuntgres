package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// GetDefaultOpClass (REL_18_6 indexcmds.c) counts exact and binary-compatible
// candidates separately. Only multiple exact candidates are catalog corruption
// (42710); nonunique compatible candidates leave selection empty (42704).
// Concrete enum/range inputs use compatible polymorphic candidates, not exacts.
func TestReferenceTransactionalDefinitionCompatibleDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, handler, input, columnType, diagnosticType, setup, items string
		inputOID                                                       int64
		values                                                         string
	}{
		{
			name: "text_varchar", handler: "bthandler", input: "text", inputOID: 25,
			columnType: "varchar", diagnosticType: "character varying",
			values: `('a',NULL,1),('z',NULL,3),(NULL,NULL,7)`,
			items:  `OPERATOR 1 pg_catalog.< (text,text), OPERATOR 2 pg_catalog.<= (text,text), OPERATOR 3 pg_catalog.= (text,text), OPERATOR 4 pg_catalog.>= (text,text), OPERATOR 5 pg_catalog.> (text,text), FUNCTION 1 pg_catalog.bttextcmp(text,text)`,
		},
		{
			name: "enum", handler: "bthandler", input: "anyenum", inputOID: 3500,
			columnType: "tx_compatible_label", diagnosticType: "tx_compatible_label",
			setup:  `CREATE TYPE tx_compatible_label AS ENUM('low','high')`,
			values: `('low',NULL,1),('high',NULL,3),(NULL,NULL,7)`,
			items:  `OPERATOR 1 pg_catalog.< (anyenum,anyenum), OPERATOR 2 pg_catalog.<= (anyenum,anyenum), OPERATOR 3 pg_catalog.= (anyenum,anyenum), OPERATOR 4 pg_catalog.>= (anyenum,anyenum), OPERATOR 5 pg_catalog.> (anyenum,anyenum), FUNCTION 1 (anyenum,anyenum) pg_catalog.enum_cmp(anyenum,anyenum)`,
		},
		{
			name: "range", handler: "gisthandler", input: "anyrange", inputOID: 3831,
			columnType: "int4range", diagnosticType: "int4range",
			values: `('[1,3)',NULL,1),('[4,8)',NULL,3),(NULL,NULL,7)`,
			items:  `OPERATOR 3 pg_catalog.&& (anyrange,anyrange), FUNCTION 1 (anyrange,anyrange) pg_catalog.range_gist_consistent(internal,anyrange,smallint,oid,internal), FUNCTION 2 (anyrange,anyrange) pg_catalog.range_gist_union(internal,internal), FUNCTION 5 (anyrange,anyrange) pg_catalog.range_gist_penalty(internal,internal,internal), FUNCTION 6 (anyrange,anyrange) pg_catalog.range_gist_picksplit(internal,internal), FUNCTION 7 (anyrange,anyrange) pg_catalog.range_gist_same(anyrange,anyrange,internal)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definitionScenario(t, []string{"tx_compatible_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.Ctx, `ROLLBACK`)
				runA := definitionRun(t, p, b, a)
				runC := definitionRun(t, p, b, c)
				if tc.setup != "" {
					runA(fixture.Exec(tc.setup, 0))
				}
				runA(fixture.Exec(`CREATE ACCESS METHOD tx_compatible_am TYPE INDEX HANDLER `+tc.handler, 0))
				runA(fixture.Exec(`CREATE TABLE tx_compatible_rows(v `+tc.columnType+`, exact_text text, unrelated integer)`, 0))
				method := definitionID(t, p, a, `SELECT oid::bigint FROM pg_am WHERE amname='tx_compatible_am'`)
				query := `CREATE INDEX tx_compatible_implicit ON tx_compatible_rows USING tx_compatible_am(v)`
				held := definitionHeld(t, p, a, query)
				missing := fixture.Diagnostic{Code: "42704", Message: `data type ` + tc.diagnosticType + ` has no default operator class for access method "tx_compatible_am"`, Hint: `You must specify an operator class for the index or define a default operator class for the data type.`}
				assertMissing := func() {
					t.Helper()
					for _, prepared := range []bool{false, true} {
						failure := fixture.ImplicitError(query, missing)
						failure.ErrorStage = "execute"
						if prepared {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
						} else {
							runA(failure)
						}
						runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_compatible_implicit'`, 0))
					}
				}
				assertMissing()
				for _, name := range []string{"first", "second"} {
					runA(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_compatible_`+name+` USING tx_compatible_am`, 0))
				}
				runA(fixture.Exec(`BEGIN`, 0))
				runC(fixture.Exec(`BEGIN`, 0))
				runA(fixture.Exec(`CREATE OPERATOR CLASS public.tx_compatible_first DEFAULT FOR TYPE `+tc.input+` USING tx_compatible_am FAMILY public.tx_compatible_first AS `+tc.items, 0))
				runC(fixture.Exec(`CREATE OPERATOR CLASS public.tx_compatible_second DEFAULT FOR TYPE `+tc.input+` USING tx_compatible_am FAMILY public.tx_compatible_second AS `+tc.items, 0))
				runA(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_compatible_first','tx_compatible_second')`, 1))
				runC(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_compatible_first','tx_compatible_second')`, 1))
				definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_compatible_first','tx_compatible_second')`, 0))
				runA(fixture.Exec(`COMMIT`, 0))
				first := definitionID(t, p, a, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_compatible_first'`)
				runA(fixture.Exec(`CREATE INDEX tx_compatible_retained ON tx_compatible_rows USING tx_compatible_am(v)`, 0))
				retained := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_compatible_retained'`)
				runA(definitionIndexProvider(retained, first, method))
				runC(fixture.Exec(`COMMIT`, 0))
				runA(fixture.Select(`SELECT opcname,opcmethod::bigint,opcintype::bigint,opcdefault FROM pg_opclass WHERE opcname IN ('tx_compatible_first','tx_compatible_second') ORDER BY opcname`, []string{"opcname", "opcmethod", "opcintype", "opcdefault"}, []string{"NAME", "INT8", "INT8", "BOOL"}, [][]any{{"tx_compatible_first", method, tc.inputOID, true}, {"tx_compatible_second", method, tc.inputOID, true}}))
				assertMissing()
				runA(definitionIndexProvider(retained, first, method))
				if tc.name == "text_varchar" {
					q := `CREATE INDEX tx_compatible_exact ON tx_compatible_rows USING tx_compatible_am(exact_text)`
					h := definitionHeld(t, p, a, q)
					for _, prepared := range []bool{false, true} {
						failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42710", Message: `there are multiple default operator classes for data type text`})
						failure.ErrorStage = "execute"
						if prepared {
							fixture.RunStep(t, p.Ctx, h, b.Recorder, failure)
						} else {
							runA(failure)
						}
						runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_compatible_exact'`, 0))
					}
				}
				if tc.name == "range" {
					// An anyrange bucket cannot compete for an integer input, even
					// when the bucket contains multiple defaults for this exact AM.
					q := `CREATE INDEX tx_compatible_unrelated ON tx_compatible_rows USING tx_compatible_am(unrelated)`
					h := definitionHeld(t, p, a, q)
					for _, prepared := range []bool{false, true} {
						failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `data type integer has no default operator class for access method "tx_compatible_am"`, Hint: missing.Hint})
						failure.ErrorStage = "execute"
						if prepared {
							fixture.RunStep(t, p.Ctx, h, b.Recorder, failure)
						} else {
							runA(failure)
						}
						runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_compatible_unrelated'`, 0))
					}
				}
				// Enumerate all index-to-class dependencies for this method,
				// not just edges whose provider happens to be the expected one.
				// Failed statements must not leave even an orphan dependency.
				runA(definitionCount(`SELECT count(*) FROM pg_depend d JOIN pg_opclass c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_opclass'::regclass AND c.opcmethod=$1::oid`, 1, method))
				for _, name := range []string{"direct", "held"} {
					q := `CREATE INDEX tx_compatible_` + name + ` ON tx_compatible_rows USING tx_compatible_am(v public.tx_compatible_first)`
					if name == "held" {
						fixture.RunStep(t, p.Ctx, definitionHeld(t, p, a, q), b.Recorder, fixture.Exec(q, 0))
					} else {
						runA(fixture.Exec(q, 0))
					}
					index := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname=$1`, "tx_compatible_"+name)
					runA(definitionIndexProvider(index, first, method))
				}
				runA(fixture.Exec(`INSERT INTO tx_compatible_rows VALUES`+tc.values, 3))
				runA(definitionIndexProvider(retained, first, method))
				runA(fixture.Exec(`DROP OPERATOR CLASS public.tx_compatible_second USING tx_compatible_am RESTRICT`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
				recovered := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_compatible_implicit'`)
				runA(definitionIndexProvider(recovered, first, method))
				runA(definitionIndexProvider(retained, first, method))
				runA(fixture.Select(`SELECT unrelated FROM tx_compatible_rows ORDER BY unrelated`, []string{"unrelated"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(7)}}))
			})
		})
	}
}
