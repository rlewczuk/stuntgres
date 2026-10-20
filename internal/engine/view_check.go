package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

type viewCheckMode uint8

const (
	viewCheckNone viewCheckMode = iota
	viewCheckLocal
	viewCheckCascaded
)

func validateViewCheckDeclaration(definition *viewDefinition) error {
	project, source, _, reason := viewWriteShape(&catalog.Relation{View: viewPayloadOf(definition)})
	if reason == "" {
		reason = "Views that have no updatable columns are not automatically updatable."
		for _, expr := range project.Expressions {
			if column, ok := viewDirectColumn(expr); ok && sameViewRelation(column.Origin, source.Ref) {
				return nil
			}
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "WITH CHECK OPTION is supported only on automatically updatable views", Hint: reason}
}
