package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteNamespaces(t *testing.T) {
	mergeUpdateAnalysisCases(t, []mergeUpdateAnalysisCase{
		{"qualifiedSource", "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND s.id=1 THEN DELETE", "s.id=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}},
		{"unqualifiedSource", "MERGE INTO t USING (SELECT id AS source_only FROM s) src ON TRUE WHEN NOT MATCHED BY SOURCE AND source_only=1 THEN DELETE", "source_only=1", fixture.Diagnostic{Code: "42703", Message: `column "source_only" does not exist`, Detail: `There is a column named "source_only" in table "src", but it cannot be referenced from this part of the query.`}},
		{"hiddenSourceAlias", "MERGE INTO t dst USING s src ON dst.id=src.id WHEN NOT MATCHED BY SOURCE AND s.id=1 THEN DELETE", "s.id=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "src", but it cannot be referenced from this part of the query.`}},
		{"hiddenTargetAlias", "MERGE INTO t dst USING s src ON dst.id=src.id WHEN NOT MATCHED BY SOURCE AND t.a=1 THEN DELETE", "t.a=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Hint: `Perhaps you meant to reference the table alias "dst".`}},
		{"matchedAmbiguous", "MERGE INTO t USING s ON t.id=s.id WHEN MATCHED AND id=1 THEN DELETE", "id=1", fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"missingTargetColumn", "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND t.missing=1 THEN DELETE", "t.missing", fixture.Diagnostic{Code: "42703", Message: `column t.missing does not exist`}},
		{"multipleHiddenSourceColumns", "MERGE INTO t USING (SELECT id AS hidden FROM s) x JOIN (SELECT id AS hidden FROM s) y ON x.hidden=y.hidden ON TRUE WHEN NOT MATCHED BY SOURCE AND hidden=1 THEN DELETE", "hidden=1", fixture.Diagnostic{Code: "42703", Message: `column "hidden" does not exist`, Detail: `There are columns named "hidden", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
	})
}

func TestReferenceMergeDeleteDiagnostics(t *testing.T) {
	mergeUpdateAnalysisCases(t, []mergeUpdateAnalysisCase{
		{"matchedUnreachable", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN DELETE WHEN MATCHED AND FALSE THEN UPDATE SET a=1", "", fixture.Diagnostic{Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"}},
		{"sourceUnreachableInterleaved", "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE AND FALSE THEN DELETE", "", fixture.Diagnostic{Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"}},
		{"whenType", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE AND 1 THEN DELETE", "1 THEN", fixture.Diagnostic{Code: "42804", Message: "argument of WHEN must be type boolean, not type integer"}},
		{"byTargetDelete", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY TARGET THEN DELETE", "DELETE", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "DELETE"`}},
		{"wrongBy", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY nope THEN DELETE", "nope", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nope"`}},
		{"missingByQualifier", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY THEN DELETE", "THEN", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "THEN"`}},
		{"deleteWhere", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN DELETE WHERE id=1", "WHERE", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "WHERE"`}},
		{"deleteFrom", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN DELETE FROM t", "FROM t", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FROM"`}},
		{"deleteUsing", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN DELETE USING s", "USING s", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "USING"`}},
	})
}

// Pinned PostgreSQL admits constant TRUE/FALSE/NULL FULL-join shortcuts;
// a non-equality predicate still requires a merge/hash-joinable condition.
func TestReferenceMergeDeleteFullJoinCapabilities(t *testing.T) {
	for _, on := range []string{"t.id=s.id", "TRUE", "t.id<s.id", "FALSE", "NULL::boolean"} {
		for _, empty := range []string{"neither", "source", "target", "both"} {
			t.Run(on+"/"+empty, func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE source(id INT)", 0))
					if empty != "target" && empty != "both" {
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					}
					if empty != "source" && empty != "both" {
						run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
					}
					q := "MERGE INTO t USING source s ON " + on + " WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40)"
					if on == "t.id<s.id" {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"})
						s.ErrorStage = "execute"
						run(s)
						var want [][]any
						if empty != "target" && empty != "both" {
							want = [][]any{{int64(1), int64(10)}}
						}
						run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
						return
					}
					var n int64
					var want [][]any
					if empty != "target" && empty != "both" {
						n++
					}
					if empty != "source" && empty != "both" && ((on != "t.id=s.id" && on != "TRUE") || empty == "target") {
						n++
						want = [][]any{{int64(1), int64(40)}}
					}
					run(fixture.Exec(q, n))
					run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}
