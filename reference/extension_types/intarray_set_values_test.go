package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySetValues(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		for _, tc := range intarraySetCases {
			t.Run(tc.name, func(t *testing.T) {
				for _, named := range []bool{false, true} {
					query := "SELECT " + tc.expression("$1::integer[]", "$2::"+tc.rightType, named) + " AS v"
					intarrayBoth(t, p, b, c, intarraySetResult(b, query, tc.family, tc.want, "{3,1,3,2}", tc.right))
					literal := "SELECT " + tc.expression("'{3,1,3,2}'::integer[]", intarraySetLiteral(tc.right, tc.rightType), named) + " AS v"
					intarrayBoth(t, p, b, c, intarraySetResult(b, literal, tc.family, tc.want))
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					for _, left := range []any{"{3,1,3,2}", []byte("{3,1,3,2}")} {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, query, tc.family, tc.want, left, tc.right))
					}
					if tc.rightType == "integer[]" {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, query, tc.family, tc.want, []byte("{3,1,3,2}"), []byte(tc.right.(string))))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, query, tc.family, nil, "{3,1,3,2}", []byte(nil)))
					}
					bad := fixture.ErrorDiagnostic(query, intarraySetNullError(), "{1,NULL,2}", tc.right)
					bad.SelectRows, bad.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
					for _, args := range [][]any{{nil, tc.right}, {"{3,1,3,2}", nil}, {nil, nil}, {[]byte(nil), tc.right}} {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, query, tc.family, nil, args...))
					}
					// Different successful input after an error/NULL cannot reuse stale data.
					want := any("{5,3}")
					switch tc.name {
					case "_int_overlap", "_int_contains", "_int_contained":
						want = false
					case "intarray_push_array":
						want = "{5,2,4,2}"
					case "intarray_del_elem":
						want = "{5}"
					case "intset_subtract":
						want = "{5}"
					case "intset_union_elem":
						want = "{3,5}"
					case "_int_union":
						want = "{2,4,5}"
					case "_int_inter":
						want = "{}"
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, query, tc.family, want, "{5}", tc.right))
				}
			})
		}
		// A genuine selected scalar function is also an ordinary one-column FROM source.
		intarrayBoth(t, p, b, c, intarraySetResult(b, "SELECT v FROM ia_provider._int_union('{3,1,3,2}'::integer[],'{2,4,2}'::integer[]) AS result(v)", "_INT4", "{1,2,3,4}"))
		for _, s := range []fixture.Step{
			fixture.Exec("SET search_path=ia_provider,public", 0),
			intarraySetResult(b, "SELECT '{3,1,3}'::integer[] | 2 AS v", "_INT4", "{1,2,3}"),
			fixture.Exec("SET search_path=public", 0),
			fixture.Exec("CREATE TABLE ia_set_metadata(id integer NOT NULL,a integer[],flag boolean)", 0),
			fixture.Exec("INSERT INTO ia_set_metadata VALUES (1,'{2,2}',true),(2,NULL,NULL)", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{" ORDER BY id", [][]any{{int64(1), "{2,2}", true}, {int64(2), nil, nil}}},
			{" WHERE id=2", [][]any{{int64(2), nil, nil}}},
			{" WHERE id=99", [][]any{}},
		} {
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,a,flag FROM ia_set_metadata"+tc.suffix, []string{"id", "a", "flag"}, []string{"INT4", "_INT4", "BOOL"}, []bool{false, true, true}, tc.rows))
		}
		for _, suffix := range []string{" WHERE id=2", " WHERE id=99"} {
			rows := [][]any{{nil, nil}}
			if suffix == " WHERE id=99" {
				rows = [][]any{}
			}
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT a OPERATOR(ia_provider.&&) a AS flag,ia_provider._int_union(a,a) AS a FROM ia_set_metadata"+suffix, []string{"flag", "a"}, []string{"BOOL", "_INT4"}, nil, rows))
		}
	})
}
