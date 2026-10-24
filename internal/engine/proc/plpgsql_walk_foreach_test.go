package proc

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

type foreachWalkHost struct {
	*walkHost
	deliveries []sqltype.Value
}

func (h *foreachWalkHost) Convert(value sqltype.Value, target sqltype.Usage, at PLOrigin) (sqltype.Value, error) {
	if at.Action == PLForeachAction {
		h.deliveries = append(h.deliveries, value)
	}
	return h.walkHost.Convert(value, target, at)
}

func foreachUnitRun(t *testing.T, source, input string) (*foreachWalkHost, *PLRun) {
	t.Helper()
	d := foreachUnitDefinition(t, source)
	h := &foreachWalkHost{walkHost: newWalkHost(t)}
	value, err := d.Inputs[0].Type.Input(context.Background(), input, -1)
	if err != nil {
		t.Fatal(err)
	}
	args := NewFrame(d.Inputs)
	args.Parameters[0] = value
	f := NewPLFrame(d.PL, d, args, sqltype.CollationRef{})
	args.Close()
	t.Cleanup(f.Close)
	return h, &PLRun{Host: h, Definition: d, Frame: f}
}

func TestPLpgSQLForeachFrame(t *testing.T) {
	for _, tc := range []struct {
		name, source, input string
		want                int64
		items               int
		found               bool
	}{
		{"sum", `DECLARE x integer; n integer:=0; BEGIN FOREACH x IN ARRAY a LOOP n:=n+x; END LOOP; RETURN n; END`, "{{1,2},{3,4}}", 10, 4, true},
		{"last", `DECLARE x integer:=99; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; RETURN x; END`, "{1,2,3}", 3, 3, true},
		{"empty", `DECLARE x integer:=99; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; RETURN x; END`, "{}", 99, 0, false},
		{"exit", `DECLARE x integer:=99; BEGIN <<f>> FOREACH x IN ARRAY a LOOP EXIT f; END LOOP f; RETURN x; END`, "{1,2,3}", 1, 1, true},
		{"continue", `DECLARE x integer; n integer:=0; BEGIN FOREACH x IN ARRAY a LOOP CONTINUE WHEN x=2; n:=n+x; END LOOP; RETURN n; END`, "{1,2,3}", 4, 3, true},
		{"return", `DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP RETURN x; END LOOP; RETURN 0; END`, "{1,2,3}", 1, 1, true},
		{"slices", `DECLARE x integer[]; n integer:=0; BEGIN FOREACH x SLICE 1 IN ARRAY a LOOP n:=n+1; END LOOP; RETURN n; END`, "[-1:0][3:4]={{1,2},{3,4}}", 2, 2, true},
		{"whole", `DECLARE x integer[]; n integer:=0; BEGIN FOREACH x SLICE 2 IN ARRAY a LOOP n:=n+1; END LOOP; RETURN n; END`, "{{1,2},{3,4}}", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, run := foreachUnitRun(t, tc.source, tc.input)
			value, flow, err := run.Block(run.Definition.PL.Root)
			got, _ := value.Integer()
			found, foundErr := walkFound(t, run).Boolean()
			if err != nil || flow.Kind != PLReturn || got != tc.want || len(h.deliveries) != tc.items || foundErr != nil || found != tc.found {
				t.Fatal("FOREACH result/delivery/FOUND", got, flow, err, len(h.deliveries), found, foundErr)
			}
			calls := 0
			for _, call := range h.calls {
				if call == "evaluate "+PLForeachAction {
					calls++
				}
			}
			if calls != 1 {
				t.Fatal("source not evaluated exactly once", calls)
			}
			if tc.name == "slices" {
				for i, slice := range h.deliveries {
					if slice.ArrayRank() != 1 || slice.ArrayDimension(0).Lower != 3 || slice.ArrayLen() != 2 {
						t.Fatal("slice shape", i, slice.ArrayDimensions())
					}
					first, _ := slice.ArrayElement(0).Integer()
					if first != int64(2*i+1) {
						t.Fatal("slice backing reused", first)
					}
				}
			}
		})
	}
}

func TestPLpgSQLForeachCancellation(t *testing.T) {
	for _, slice := range []bool{false, true} {
		for _, body := range []string{"", "CONTINUE;"} {
			declaration, clause := "integer", ""
			if slice {
				declaration, clause = "integer[]", " SLICE 1"
			}
			h, run := foreachUnitRun(t, "DECLARE x "+declaration+"; BEGIN FOREACH x"+clause+" IN ARRAY a LOOP "+body+" END LOOP; RETURN 0; END", "{{1,2},{3,4}}")
			ctx, cancel := context.WithCancel(context.Background())
			h.ctx = &assignedCancelContext{Context: ctx, cancel: cancel, assigned: func() bool { return len(h.deliveries) != 0 }}
			_, _, err := run.Block(run.Definition.PL.Root)
			cancel()
			found, _ := walkFound(t, run).Boolean()
			if !errors.Is(err, context.Canceled) || len(h.deliveries) != 1 || found {
				t.Fatal("active FOREACH did not fence empty/continue body", err, len(h.deliveries), found)
			}
		}
	}
}
