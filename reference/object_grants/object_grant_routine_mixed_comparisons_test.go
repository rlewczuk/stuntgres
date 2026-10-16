package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineMixedComparisons(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		type witness struct {
			target, function, expression, control string
			rows, controlRows                     [][]any
		}
		trueFalse := [][]any{{true}, {false}}
		trueTrue := [][]any{{true}, {true}}
		cases := []witness{
			{"pg_catalog.float8eq(double precision,double precision)", "float8eq", "n=d", "i=n", trueFalse, trueTrue},
			{"pg_catalog.numeric_eq(numeric,numeric)", "numeric_eq", "i=n", "n=d", trueTrue, trueFalse},
			{"pg_catalog.float84eq(double precision,real)", "float84eq", "n=r", "r=n", trueFalse, trueFalse},
			{"pg_catalog.float48eq(real,double precision)", "float48eq", "r=n", "n=r", trueFalse, trueFalse},
			{"pg_catalog.texteq(text,text)", "texteq", "t=b", "v=b", trueFalse, trueTrue},
			{"pg_catalog.bpchareq(character,character)", "bpchareq", "v=b", "t=b", trueTrue, trueFalse},
			{"pg_catalog.oideq(oid,oid)", "oideq", "rc='pg_catalog.pg_class'::regclass", "i=n", trueFalse, trueTrue},
			{"pg_catalog.float8lt(double precision,double precision)", "float8lt", "n<d", "n=d", [][]any{{false}, {true}}, trueFalse},
		}
		targets := make([]string, len(cases))
		for i, item := range cases {
			targets[i] = item.target
		}
		defer objectGrantRoutineCleanup(t, p, c, targets)
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_mixed_source(k INT,i INT,n NUMERIC,d DOUBLE PRECISION,r REAL,t TEXT,v VARCHAR,b CHAR(3),rc REGCLASS)", 0))
		run(fixture.Exec("INSERT INTO og_mixed_source VALUES(1,1,1,1,1,'a','a ','a','pg_catalog.pg_class'::regclass),(2,2,2,3,3,'b ','b ','b','pg_catalog.pg_namespace'::regclass)", 2))
		run(fixture.Exec("GRANT SELECT ON og_mixed_source TO og_object_reader", 0))
		query := func(expression string) string {
			return "SELECT " + expression + " AS v FROM og_mixed_source ORDER BY k"
		}
		selectRows := func(expression string, rows [][]any) {
			run(fixture.Select(query(expression), []string{"v"}, []string{"BOOL"}, rows))
		}
		for _, item := range cases {
			fixture.RoleGrantActor(run, "og_object_reader", func() { selectRows(item.expression, item.rows) })
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+item.target+" FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, query(item.expression), "function", item.function)
				selectRows(item.control, item.controlRows)
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+item.target+" TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() { selectRows(item.expression, item.rows) })
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			selectRows("rc='1259'", trueFalse)
			selectRows("n='1'", trueFalse)
			selectRows("v='a '", trueFalse)
			selectRows("b='a '", trueFalse)
			step := fixture.ImplicitError(query("rc='pg_catalog.pg_class'"), fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "pg_catalog.pg_class"`, Position: "11"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step.ErrorStage = "prepare"
			}
			run(step)
		})
	})
}
