package parser

import "testing"

func TestSequenceOwnerOperands(t *testing.T) {
	for _, operand := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER", `"current_role"`, `"current_user"`, `"session_user"`} {
		t.Run(operand, func(t *testing.T) {
			for _, prefix := range []string{"ALTER SEQUENCE s OWNER TO ", "ALTER SEQUENCE IF EXISTS public.s OWNER TO "} {
				ss, err := Parse(prefix + operand)
				if err != nil || len(ss) != 1 {
					t.Fatalf("%s: statements=%v error=%v", prefix+operand, ss, err)
				}
				s, ok := ss[0].(*AlterSequence)
				if !ok || s.Action != "owner" || s.Target.Text == "" {
					t.Fatalf("owner AST: %#v", ss[0])
				}
				if s.OwnerIdentity != (operand[0] != '"') {
					t.Fatalf("%s: OwnerIdentity=%v", operand, s.OwnerIdentity)
				}
			}
		})
	}
}
