package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestRuleModeDump(t *testing.T) {
	for _, action := range []string{"ENABLE RULE", "DISABLE RULE", "ENABLE REPLICA RULE", "ENABLE ALWAYS RULE"} {
		t.Run(action, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE public.\"old table\"(id INT)", "CREATE RULE \"old rule\" AS ON INSERT TO public.\"old table\" DO NOTHING", "ALTER TABLE public.\"old table\" " + action + " \"old rule\"", "ALTER RULE \"old rule\" ON public.\"old table\" RENAME TO \"new rule\"", "ALTER TABLE public.\"old table\" RENAME TO \"new table\""} {
				sessionSQL(t, s, q)
			}
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			groups, err := stored.RuleDumpGroups(context.Background(), e.CatalogRead(), auth.Roles)
			if err != nil || len(groups) != 1 {
				t.Fatal(groups, err)
			}
			want := "CREATE RULE \"new rule\" AS ON INSERT TO \"public\".\"new table\" DO ALSO NOTHING;\n"
			if action != "ENABLE RULE" {
				want += "ALTER TABLE \"public\".\"new table\" " + action + " \"new rule\";\n"
			}
			if groups[0].SQL != want || groups[0].Table != "\"public\".\"new table\"" {
				t.Fatalf("got %+v want SQL %q", groups[0], want)
			}
		})
	}
	for _, tc := range []struct {
		q    string
		want bool
	}{
		{"ALTER TABLE public.t DISABLE RULE r", true},
		{"ALTER TABLE public.t ENABLE REPLICA RULE r", true},
		{"ALTER TABLE public.t ENABLE ALWAYS RULE r", true},
		{"ALTER TABLE public.t ENABLE RULE r", false},
		{"ALTER TABLE t DISABLE RULE r", false},
		{"ALTER TABLE ONLY public.t DISABLE RULE r", false},
		{"ALTER TABLE IF EXISTS public.t DISABLE RULE r", false},
		{"ALTER TABLE public.t DISABLE RULE r, ENABLE ALWAYS RULE r", false},
	} {
		if got := table.RestoreRuleMode(enginetest.Statement(t, tc.q).(*parser.AlterTable)); got != tc.want {
			t.Fatal(tc.q, got, tc.want)
		}
	}
}
