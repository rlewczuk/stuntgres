package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayExpressionAssignments(t *testing.T) {
	cases := []struct {
		name, set string
		id        int
		want      string
	}{
		{"extend_right", "a[5]=99", 1, "{10,NULL,30,NULL,99}"},
		{"extend_left", "a[-1]=9", 1, "[-1:3]={9,NULL,10,NULL,30}"},
		{"null_cell", "a[2]=NULL", 1, "{10,NULL,30}"},
		{"matrix_element", "m[2][1]=9", 1, "{{1,2},{9,4}}"},
		{"flatten_source", "m[1:2][1:1]=ARRAY[8,9]", 1, "{{8,2},{9,4}}"},
		{"excess_source", "a[1:2]=ARRAY[[7,8],[9,10]]", 1, "{7,8,30}"},
		{"null_slice", "a[1:2]=NULL", 1, "{10,NULL,30}"},
		{"null_base_slice", "a[1:2]=NULL", 4, "{}"},
		{"omitted_lower", "a[:2]=ARRAY[7,8]", 1, "{7,8,30}"},
		{"old_row", "a[2]=a[1],a[1]=a[2]", 1, "{NULL,10,30}"},
		{"overlap", "a[1:2]=ARRAY[7,8],a[2:3]=ARRAY[9,10]", 1, "{7,9,10}"},
		{"duplicate_partial", "a[1]=7,a[1]=8", 1, "{8,NULL,30}"},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			name := tc.name
			if prepared {
				name += "_prepared"
			}
			t.Run(name, func(t *testing.T) {
				arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					arrayExpressionFixture(t, p, b, c)
					q := "UPDATE public.ax SET " + tc.set + " WHERE id=$1"
					s := fixture.Exec(q, 1, tc.id)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					col := "a"
					if tc.set[0] == 'm' {
						col = "m"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect("SELECT "+col+" FROM public.ax WHERE id=$1", []string{col}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG, tc.id))
				})
			})
		}
	}
	t.Run("insert_targets", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE public.ax_insert (id INT,a INT[] DEFAULT ARRAY[99],w BIGINT[])", 0))
			run(fixture.Exec("INSERT INTO public.ax_insert(id,a[3]) VALUES(1,7)", 1))
			run(fixture.Prepared(fixture.Exec("INSERT INTO public.ax_insert(id,a[-1:0]) VALUES(2,ARRAY[8,9])", 1)))
			run(fixture.Exec("INSERT INTO public.ax_insert(id,a[1],a[3]) VALUES(3,7,9)", 1))
			run(fixture.Prepared(fixture.Exec("INSERT INTO public.ax_insert(id,a[2][4]) VALUES(4,NULL)", 1)))
			run(fixture.Exec("INSERT INTO public.ax_insert(id,a[2]) SELECT 5,v FROM public.one", 1))
			run(fixture.Exec("INSERT INTO public.ax_insert(id,w) VALUES(6,ARRAY[1,2])", 1))
			run(fixture.ArrayReferenceSelect("SELECT id,a,w FROM public.ax_insert ORDER BY id", []string{"id", "a", "w"}, []string{"INT4", "_INT4", "_INT8"}, []string{"INT4", "_INT4", "_INT8"}, [][]any{{int64(1), "[3:3]={7}", nil}, {int64(2), "[-1:0]={8,9}", nil}, {int64(3), "{7,NULL,9}", nil}, {int64(4), "[2:2][4:4]={{NULL}}", nil}, {int64(5), "[2:2]={1}", nil}, {int64(6), "{99}", "{1,2}"}}, b.PG))
		})
	})
	t.Run("atomicity", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			arrayExpressionFixture(t, p, b, c)
			check := fixture.ArrayReferenceSelect("SELECT id,a FROM public.ax ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{10,NULL,30}"}, {int64(2), "[0:2]={40,50,60}"}, {int64(3), "{}"}, {int64(4), nil}}, b.PG)
			for _, q := range []string{"UPDATE public.ax SET a[1:2]=CASE WHEN id=1 THEN ARRAY[7,8] ELSE ARRAY[9] END WHERE id<3", "INSERT INTO public.ax(id,a[1:2]) VALUES(5,ARRAY[7,8]),(6,ARRAY[9])"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "source array too small")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
		})
	})
}

func TestReferenceArrayExpressionDiagnostics(t *testing.T) {
	cases := []struct {
		name, q, code, message, position, hint, detail string
		analysis                                       bool
	}{
		{"empty", "SELECT ARRAY[] FROM public.one", "42P18", "cannot determine type of empty array", "8", "Explicitly cast to the desired type, for example ARRAY[]::integer[].", "", true},
		{"category", "SELECT ARRAY[1,TRUE] FROM public.one", "42804", "ARRAY types integer and boolean cannot be matched", "16", "", "", true},
		{"bad_cell", "SELECT ARRAY[1,'oops'] FROM public.one", "22P02", `invalid input syntax for type integer: "oops"`, "16", "", "", true},
		{"utf8_empty", "/*é*/ SELECT ARRAY[] FROM public.one", "42P18", "cannot determine type of empty array", "14", "Explicitly cast to the desired type, for example ARRAY[]::integer[].", "", true},
		{"boolean_index", "SELECT a[TRUE] FROM public.ax", "42804", "array subscript must have type integer", "10", "", "", true},
		{"text_index", "SELECT a['1'::text] FROM public.ax", "42804", "array subscript must have type integer", "10", "", "", true},
		{"scalar_base", "SELECT id[1] FROM public.ax", "42804", "cannot subscript type integer because it does not support subscripting", "8", "", "", true},
		{"rank_limit", "SELECT m[1][1][1][1][1][1][1] FROM public.ax", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)", "", "", "", true},
		{"null_assignment", "UPDATE public.ax SET a[NULL]=7 WHERE id=1", "22004", "array subscript in assignment must not be null", "", "", "", false},
		{"matrix_enlarge", "UPDATE public.ax SET m[3][1]=7 WHERE id=1", "2202E", "array subscript out of range", "", "", "", false},
		{"wrong_rank", "UPDATE public.ax SET m[1]=7 WHERE id=1", "2202E", "wrong number of array subscripts", "", "", "", false},
		{"inverted", "UPDATE public.ax SET a[2:1]=ARRAY[7] WHERE id=1", "2202E", "upper bound cannot be less than lower bound", "", "", "", false},
		{"short_source", "UPDATE public.ax SET a[1:2]=ARRAY[7] WHERE id=1", "2202E", "source array too small", "", "", "", false},
		{"empty_omission", "UPDATE public.ax SET a[:2]=ARRAY[7,8] WHERE id=4", "2202E", "array slice subscript must provide both boundaries", "", "", "When assigning to a slice of an empty array value, slice boundaries must be fully specified.", false},
		{"element_type", "UPDATE public.ax SET a[1]=TRUE WHERE id=1", "42804", `subscripted assignment to "a" requires type integer but expression is of type boolean`, "22", "You will need to rewrite or cast the expression.", "", true},
		{"partial_default", "UPDATE public.ax SET a[1]=DEFAULT WHERE id=1", "0A000", "cannot set an array element to DEFAULT", "22", "", "", true},
		{"ragged", "SELECT ARRAY[ARRAY[1],ARRAY[2,3]] FROM public.one", "2202E", "multidimensional arrays must have array expressions with matching dimensions", "", "", "", false},
		{"null_child", "SELECT ARRAY[ARRAY[1],NULL::int[]] FROM public.one", "2202E", "multidimensional arrays must have array expressions with matching dimensions", "", "", "", false},
		{"empty_child", "SELECT ARRAY[ARRAY[1],ARRAY[]::int[]] FROM public.one", "2202E", "multidimensional arrays must have array expressions with matching dimensions", "", "", "", false},
		{"child_bounds", "SELECT ARRAY['[0:1]={1,2}'::int[],'[1:2]={3,4}'::int[]] FROM public.one", "2202E", "multidimensional arrays must have array expressions with matching dimensions", "", "", "", false},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			name := tc.name
			if prepared {
				name += "_prepared"
			}
			t.Run(name, func(t *testing.T) {
				arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					arrayExpressionFixture(t, p, b, c)
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Position = tc.position
					want.Hint = tc.hint
					want.Detail = tc.detail
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.Prepared = prepared
					s.SelectRows = tc.q[0] != 'U'
					s.ErrorStage = "execute"
					if prepared && tc.analysis {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			})
		}
	}
}
