package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
)

func TestQuerySetCounts(t *testing.T) {
	for _, tc := range []struct {
		left, right                                                 int
		unionAll, union, intersectAll, intersect, exceptAll, except int
	}{
		{0, 0, 0, 0, 0, 0, 0, 0}, {0, 3, 3, 1, 0, 0, 0, 0}, {3, 0, 3, 1, 0, 0, 3, 1},
		{3, 2, 5, 1, 2, 1, 1, 0}, {2, 3, 5, 1, 2, 1, 0, 0}, {3, 3, 6, 1, 3, 1, 0, 0},
		{7, 3, 10, 1, 3, 1, 4, 0},
	} {
		for _, op := range []struct {
			name string
			all  bool
			want int
		}{
			{"union", true, tc.unionAll}, {"union", false, tc.union}, {"intersect", true, tc.intersectAll},
			{"intersect", false, tc.intersect}, {"except", true, tc.exceptAll}, {"except", false, tc.except},
		} {
			if got := exec.SetMultiplicity(op.name, op.all, tc.left, tc.right); got != op.want {
				t.Fatalf("%s ALL=%t m=%d n=%d got%d want%d", op.name, op.all, tc.left, tc.right, got, op.want)
			}
		}
	}
}

func TestQuerySetArmCleanupErrorPriority(t *testing.T) {
	primary, cleanup := errors.New("primary arm failure"), errors.New("arm close failure")
	for _, failure := range []error{primary, nil} {
		closes := 0
		arm := exec.SetArm{Inner: exec.Stage{Selection: []int{}, Produce: func(context.Context, *exec.Stage) (bool, error) { return false, failure }, Finish: func() error { closes++; return cleanup }}}
		_, _, err := arm.Next(context.Background())
		if failure != nil {
			if err != primary {
				t.Fatal("cleanup replaced primary", err)
			}
			if err := arm.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := arm.Close(); err != cleanup {
			t.Fatal("successful arm lost required cleanup failure", err)
		}
		if err := arm.Close(); err != nil || closes != 1 {
			t.Fatal("arm closed more than once", closes, err)
		}
	}
}
