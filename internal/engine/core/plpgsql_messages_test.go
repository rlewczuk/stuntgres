package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLAssertRuntime(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{"ASSERT true,missing_message();", ""},
		{"ASSERT false,'failed';", "P0004"},
		{"ASSERT NULL,NULL;", "P0004"},
		{"ASSERT 1/0=0;", "22012"},
		{"ASSERT false,1/0;", "22012"},
		{"ASSERT missing_condition(),missing_message();", "42883"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			e, s := plUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, "BEGIN "+tc.body+" RETURN 7; END")
			input := proc.NewFrame(nil)
			defer input.Close()
			read := e.CatalogRead()
			setting, err := e.Settings.Lookup("plpgsql.check_asserts")
			if err != nil {
				t.Fatal("assert setting not registered", err)
			}
			originalRoutine, originalDepth := core.ScopeSQLRoutine(s), core.ScopeActiveSQLRoutineDepth(s)
			for _, enabled := range []bool{false, true, false, true} {
				var inputSetting *string
				off := "off"
				if !enabled {
					inputSetting = &off
				}
				if _, err := core.EngineApplySetting(e, s.Op, s.Op.Session.Settings, setting, inputSetting, false); err != nil {
					t.Fatal(err)
				}
				value, err := core.ScopeInvokePLRoutine(s, d, input, read, sqltype.CollationRef{})
				want := tc.code
				if !enabled {
					want = ""
				}
				if want == "" {
					if err != nil {
						t.Fatal(err)
					}
					if n, err := value.Integer(); err != nil || n != 7 {
						t.Fatal("assert changed return", n, err)
					}
				} else {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != want {
						t.Fatal("reached assertion diagnostic", want, err)
					}
				}
				if core.ScopeActiveSQLRoutineDepth(s) != originalDepth || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != core.RoutineDemandLexical(&originalRoutine) {
					t.Fatal("assert leaked lexical activation or depth")
				}
			}
		})
	}
}
