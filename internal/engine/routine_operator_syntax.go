package engine

import (
	"fmt"

	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// operatorOptionsSyntax checks CREATE/ALTER OPERATOR options before any lookup.
// An unknown CREATE option is a warning, as in PostgreSQL.
func operatorOptionsSyntax(h routine.DDLHost, options []parser.OperatorOption, alter bool) error {
	for _, raw := range options {
		o := raw
		if alter {
			o = routine.OperatorAlterOption(o)
			if o.HasValue {
				if _, err := routine.OperatorOptionName(o); err != nil {
					return err
				}
			}
		}
		switch o.Name {
		case "leftarg", "rightarg":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" cannot be changed`, o.Name))
			}
			typ, err := routine.OperatorOptionType(o)
			if err != nil {
				return err
			}
			if typ.Setof {
				return sqlError("42P13", "SETOF type not allowed for operator argument")
			}
		case "function", "procedure":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" cannot be changed`, o.Name))
			}
			if _, err := routine.OperatorOptionName(o); err != nil {
				return err
			}
		case "restrict", "join", "commutator", "negator":
			if alter && !o.HasValue && (o.Name == "restrict" || o.Name == "join") {
				continue
			}
			if _, err := routine.OperatorOptionName(o); err != nil {
				return err
			}
		case "hashes", "merges":
			if _, err := routine.OperatorBoolean(o); err != nil {
				return err
			}
		case "sort1", "sort2", "ltcmp", "gtcmp":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" not recognized`, o.Name))
			}
		default:
			message := fmt.Sprintf(`operator attribute "%s" not recognized`, o.Name)
			if alter {
				return sqlError("42601", message)
			}
			h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42601", Message: message})
		}
	}
	return nil
}
