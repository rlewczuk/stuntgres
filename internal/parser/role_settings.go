package parser

// RenameRole retains the execute-time source identity and decoded destination.
type RenameRole struct {
	Role RoleSpec
	Name Identifier
	Pos  Position
}

func (*RenameRole) statement() {}

// AlterRoleSettings changes one persisted scope, not the issuing session.
// Exactly one of Set and Reset is present. FROM CURRENT retains only Set.Name.
type AlterRoleSettings struct {
	Role        RoleSpec
	All         bool
	Database    *Identifier
	Set         *Set
	Reset       *Reset
	FromCurrent bool
	Pos         Position
}

func (*AlterRoleSettings) statement() {}

func roleSettingSpec(t lexeme) RoleSpec {
	s := RoleSpec{Name: t.text, Pos: t.pos}
	if t.keyword != nil {
		switch t.text {
		case "current_user", "current_role":
			s.Name = ""
			s.Identity = "current"
		case "session_user":
			s.Name = ""
			s.Identity = "session"
		}
	}
	return s
}
