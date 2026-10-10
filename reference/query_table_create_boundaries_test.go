package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceQueryTableCreateBoundaries(t *testing.T) {
	cases := []struct{ name, query, token, message string }{
		{"temp", "CREATE TEMP TABLE qc_boundary AS SELECT 1 AS i", "TEMP", "CREATE syntax is unsupported"},
		{"temporary", "CREATE TEMPORARY TABLE qc_boundary AS SELECT 1 AS i", "TEMPORARY", "CREATE syntax is unsupported"},
		{"unlogged", "CREATE UNLOGGED TABLE qc_boundary AS SELECT 1 AS i", "UNLOGGED", "CREATE syntax is unsupported"},
		{"temp_schema", "CREATE TABLE pg_temp.qc_boundary AS SELECT 1 AS i", "pg_temp", "temporary schema creation is unsupported"},
		{"access_method", "CREATE TABLE qc_boundary USING heap AS SELECT 1 AS i", "USING", "table access methods are unsupported"},
		{"external_method", "CREATE TABLE qc_boundary USING qc_external AS SELECT 1 AS i", "USING", "table access methods are unsupported"},
		{"storage", "CREATE TABLE qc_boundary WITH(fillfactor=70) AS SELECT 1 AS i", "WITH", "table storage parameters are unsupported"},
		{"tablespace", "CREATE TABLE qc_boundary TABLESPACE pg_default AS SELECT 1 AS i", "TABLESPACE", "table tablespaces are unsupported"},
		{"on_commit", "CREATE TEMP TABLE qc_boundary ON COMMIT PRESERVE ROWS AS SELECT 1 AS i", "TEMP", "CREATE syntax is unsupported"},
		{"on_commit_regular", "CREATE TABLE qc_boundary ON COMMIT PRESERVE ROWS AS SELECT 1 AS i", "ON", "ON COMMIT is unsupported"},
		{"execute", "CREATE TABLE qc_boundary AS EXECUTE qc_prepared", "EXECUTE", "CREATE TABLE AS EXECUTE is unsupported"},
		{"table", "CREATE TABLE qc_boundary AS TABLE qc_source", "TABLE", "TABLE queries are unsupported"},
		{"into_temp", "SELECT 1 AS i INTO TEMP qc_boundary", "TEMP", "temporary tables are unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					_, run := queryTableCreateSession(t, p, b)
					queryTableCreateSource(run)
					if b.pg {
						if tc.name == "execute" {
							run(referenceExec("PREPARE qc_prepared AS SELECT 1 AS i", 0))
						}
						count := int64(1)
						if tc.name == "table" {
							count = 3
						}
						if tc.name == "external_method" {
							run(referenceImplicitError(tc.query, referenceDiagnostic{Code: "42704", Message: `access method "qc_external" does not exist`}))
							queryTableCreateAbsent(t, run, b.pg, "qc_boundary")
						} else if tc.name == "on_commit_regular" {
							run(referenceImplicitError(tc.query, referenceDiagnostic{Code: "42P16", Message: "ON COMMIT can only be used on temporary tables"}))
							queryTableCreateAbsent(t, run, b.pg, "qc_boundary")
						} else {
							run(referenceExec(tc.query, count))
							if tc.name == "table" {
								run(queryTableCreateBag("qc_boundary"))
							} else {
								run(referenceSelect("SELECT i FROM qc_boundary", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
							}
						}
						if tc.name == "execute" {
							run(referenceExec("DEALLOCATE qc_prepared", 0))
						}
					} else {
						want := controlsReferenceDiagnostic("0A000", tc.message)
						at := strings.Index(tc.query, tc.token)
						if tc.name == "table" {
							at = strings.LastIndex(tc.query, "TABLE")
						}
						want.Position = referencePosition(at + 1)
						run(referenceErrorDiagnostic(tc.query, want))
						queryTableCreateAbsent(t, run, b.pg, "qc_boundary")
					}
					run(queryTableCreateBag("qc_source"))
				})
			}
		})
	}
}

func TestReferenceQueryTableCreateWholeInput(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO qc_sentinel VALUES(2); CREATE TABLE qc_bad AS SELECT 1 AS i WITH BANANAS",
		"INSERT INTO qc_sentinel VALUES(2); SELECT 1 AS i INTO qc_bad WITH NO DATA",
		"INSERT INTO qc_sentinel VALUES(2); CREATE TABLE qc_bad AS SELECT i FROM (SELECT i INTO qc_nested FROM qc_source) d",
	} {
		t.Run(q, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					_, run := queryTableCreateSession(t, p, b)
					queryTableCreateSource(run)
					run(referenceExec("CREATE TABLE qc_sentinel(i INT)", 0))
					run(referenceExec("INSERT INTO qc_sentinel VALUES(1)", 1))
					want := controlsReferenceDiagnostic("42601", `syntax error at or near "WITH"`)
					want.Position = referencePosition(strings.LastIndex(q, "WITH") + 1)
					if strings.Contains(q, "BANANAS") {
						want.Message = `syntax error at or near "BANANAS"`
						want.Position = referencePosition(strings.Index(q, "BANANAS") + 1)
					}
					if strings.Contains(q, "qc_nested") {
						want.Message = "SELECT ... INTO is not allowed here"
						want.Position = referencePosition(strings.Index(q, "qc_nested") + 1)
					}
					run(referenceErrorDiagnostic(q, want))
					queryTableCreateAbsent(t, run, b.pg, "qc_bad")
					queryTableCreateAbsent(t, run, b.pg, "qc_nested")
					run(referenceSelect("SELECT i FROM qc_sentinel", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}
