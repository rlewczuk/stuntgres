package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientMinMessagesLifecycle(t *testing.T) {
	type boundary struct{ query, value string }
	cases := []struct {
		name  string
		steps []boundary
	}{
		{"set-rollback", []boundary{{"BEGIN", "notice"}, {"SET client_min_messages TO warning", "warning"}, {"ROLLBACK", "notice"}}},
		{"set-commit", []boundary{{"BEGIN", "notice"}, {"SET client_min_messages TO warning", "warning"}, {"COMMIT", "warning"}}},
		{"local-commit", []boundary{{"BEGIN", "notice"}, {"SET LOCAL client_min_messages TO warning", "warning"}, {"COMMIT", "notice"}}},
		{"local-rollback", []boundary{{"BEGIN", "notice"}, {"SET LOCAL client_min_messages TO warning", "warning"}, {"ROLLBACK", "notice"}}},
		{"savepoint-set", []boundary{{"BEGIN", "notice"}, {"SAVEPOINT cmm_s", "notice"}, {"SET client_min_messages TO warning", "warning"}, {"ROLLBACK TO cmm_s", "notice"}, {"ROLLBACK", "notice"}}},
		{"savepoint-local", []boundary{{"BEGIN", "notice"}, {"SET client_min_messages TO warning", "warning"}, {"SAVEPOINT cmm_s", "warning"}, {"SET LOCAL client_min_messages TO notice", "notice"}, {"ROLLBACK TO cmm_s", "warning"}, {"ROLLBACK", "notice"}}},
	}
	for _, tc := range cases {
		for _, mode := range []string{"direct", "prepared"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := cmmOpen(t, p, backend.PG), cmmOpen(t, p, backend.PG)
						a.setup()
						b.set("notice", false)
						a.set("warning", mode == "prepared")
						a.read("warning", false, true)
						a.effect("warning", nil)
						b.read("notice", false, true)
						b.effect("notice", nil)
						a.set("notice", false)
						held := a.prepare(cmmProducer)
						for _, step := range tc.steps {
							command := fixture.Exec(step.query, 0)
							command.Prepared = mode == "prepared" && strings.HasPrefix(step.query, "SET ")
							a.run(command)
							a.read(step.value, mode == "prepared", false)
							a.effect(step.value, nil)
							a.effect(step.value, &held)
							b.read("notice", mode == "prepared", false)
							b.effect("notice", nil)
						}
					})
				}
			})
		}
	}
}
