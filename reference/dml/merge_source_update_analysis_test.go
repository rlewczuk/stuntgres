package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL18.0 pins namespace diagnostics, UTF8 positions, and execution-only
// implicit FULL planning failures. Both routes retain the identical SQL source.
func TestReferenceMergeSourceUpdateAnalysis(t *testing.T) {
	for _, tc := range []struct {
		name, source, on, arms string
		args                   []any
		empty                  bool
	}{
		{"qualifiedWhen", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE AND s.x>0 THEN UPDATE SET v=1", nil, false},
		{"qualifiedRHSFalseEmpty", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET v=s.x", nil, true},
		{"correlatedScalar", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT s.x)", nil, false},
		{"correlatedWhen", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE AND (SELECT s.x)>0 THEN UPDATE SET v=1", nil, false},
		{"arrayLower", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a[(SELECT s.i):2]=ARRAY[7,8]", nil, false},
		{"arrayUpper", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a[1:(SELECT s.i)]=ARRAY[7,8]", nil, false},
		{"unqualifiedSource", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=x", nil, false},
		{"derivedSource", "(SELECT id,x,i FROM source) s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT s.x)", nil, false},
		{"nestedLocalShadow", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT s.x FROM source s WHERE s.id=1)", nil, false},
		{"independentRelation", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT max(x) FROM source independent)", nil, false},
		{"unknownQualifier", "source s", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=unknown.x", nil, false},
		{"targetAliasHidden", "source s", "dst.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v", nil, false},
		{"fullInequality", "source s", "t.id<s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullInequalityFalseEmpty", "source s", "t.id<s.id", "WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET AND FALSE THEN INSERT(id,v) VALUES(s.id,s.x)", nil, true},
		{"fullInequalityEmptyTarget", "source s", "t.id<s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullInequalityBothEmpty", "source s", "t.id<s.id", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, true},
		{"fullOr", "source s", "t.id=s.id OR t.v=s.x", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullEqualityResidual", "source s", "t.id=s.id AND t.v>s.x", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullTrue", "source s", "TRUE", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullFalse", "source s", "FALSE", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", nil, false},
		{"fullParameterTrue", "source s", "$1::boolean", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", []any{true}, false},
		{"fullParameterFalse", "source s", "$1::boolean", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", []any{false}, false},
		{"fullParameterNull", "source s", "$1::boolean", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,s.x)", []any{nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT,a INT[])", 0))
				run(fixture.Exec("CREATE TABLE source(id INT,x INT,i INT)", 0))
				emptyTarget := tc.name == "fullInequalityEmptyTarget" || tc.name == "fullInequalityBothEmpty"
				if !emptyTarget {
					run(fixture.Exec("INSERT INTO t VALUES(2,10,ARRAY[10,20])", 1))
				}
				if !tc.empty {
					run(fixture.Exec("INSERT INTO source VALUES(1,7,1)", 1))
				}
				target := "t"
				if tc.name == "targetAliasHidden" {
					target = "t dst"
				}
				q := "/* é */ MERGE INTO " + target + " USING " + tc.source + " ON " + tc.on + " " + tc.arms
				d := fixture.Diagnostic{}
				switch tc.name {
				case "qualifiedWhen", "qualifiedRHSFalseEmpty", "correlatedScalar", "correlatedWhen", "arrayLower", "arrayUpper", "derivedSource":
					d = fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}
					d.Position = map[string]string{"qualifiedWhen": "81", "qualifiedRHSFalseEmpty": "105", "correlatedScalar": "103", "correlatedWhen": "89", "arrayLower": "103", "arrayUpper": "105", "derivedSource": "124"}[tc.name]
				case "unqualifiedSource":
					d = fixture.Diagnostic{Code: "42703", Message: `column "x" does not exist`, Detail: `There is a column named "x" in table "s", but it cannot be referenced from this part of the query.`, Position: "95"}
				case "unknownQualifier":
					d = fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "unknown"`, Position: "95"}
				case "targetAliasHidden":
					d = fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Hint: `Perhaps you meant to reference the table alias "dst".`, Position: "101"}
				case "fullInequality", "fullInequalityFalseEmpty", "fullInequalityEmptyTarget", "fullInequalityBothEmpty", "fullOr":
					d = fixture.Diagnostic{Code: "0A000", Message: "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"}
				}
				if d.Code != "" {
					s := fixture.ImplicitError(q, d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && d.Code != "0A000" {
						s.ErrorStage = "prepare"
					}
					run(s)
					want := [][]any{{int64(2), int64(10), "{10,20}"}}
					if emptyTarget {
						want = [][]any{}
					}
					run(fixture.ArrayReferenceSelect("SELECT id,v,a FROM t", []string{"id", "v", "a"}, []string{"INT4", "INT4", "_INT4"}, []string{"INT4", "INT4", "_INT4"}, want, b.PG))
				} else {
					n := int64(2)
					want := [][]any{{int64(1), int64(7)}, {int64(2), int64(11)}}
					switch tc.name {
					case "nestedLocalShadow", "independentRelation":
						n = 1
						want = [][]any{{int64(2), int64(7)}}
					case "fullTrue", "fullParameterTrue":
						n = 0
						want = [][]any{{int64(2), int64(10)}}
					}
					s := fixture.Exec(q, n, tc.args...)
					s.Prepared = prepared
					// The leading UTF8 comment intentionally bypasses MergeRun's
					// command-prefix detection, so specify the pq tag boundary.
					if b.PG {
						s.Affected = 0
					}
					run(s)
					run(fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				}
			})
		})
	}
}
