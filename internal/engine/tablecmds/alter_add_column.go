package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The ADD COLUMN candidate runs under Engine.mu; DEFAULT recipes keep the
// statement's authority for row fills.
func AddColumnCandidate(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, table.AddedColumn, error) {
	scope.RoutineAuth = auth
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.AddColumnCandidate(AlterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, old, action, captured)
}

// remapAdmittedAdds translates actual admissions across a stable-order action
// projection. ADD definitions retain their syntax identity through preparation;
// names and pre-statement relation shape are not evidence of admission.
func remapAdmittedAdds(admitted []int, executed, original []parser.AlterTableAction) error {
	source := 0
	for i, actionIndex := range admitted {
		definition := executed[actionIndex].Definition
		if definition == nil {
			return fmt.Errorf("stuntgres: admitted ADD has no column definition")
		}
		for source < len(original) && (original[source].Action != parser.AddColumn || original[source].Definition != definition) {
			source++
		}
		if source == len(original) {
			return fmt.Errorf("stuntgres: admitted ADD lost its original action")
		}
		admitted[i] = source
		source++
	}
	return nil
}
