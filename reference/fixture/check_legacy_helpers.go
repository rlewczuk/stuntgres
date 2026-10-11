package fixture

import (
	"testing"
)

func CheckBackends(t *testing.T, scenario func(*testing.T, func(Step))) {
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			scenario(t, func(s Step) { RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
		})
	}
}
