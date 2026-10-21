package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

type viewCheckMode uint8

const (
	CheckNone viewCheckMode = iota
	CheckLocal
	CheckCascaded
)

func ValidateViewCheckDeclaration(definition *viewDefinition) error {
	project, source, _, reason := ViewWriteShape(&catalog.Relation{View: ViewPayload(definition)})
	if reason == "" {
		reason = "Views that have no updatable columns are not automatically updatable."
		for _, expr := range project.Expressions {
			if column, ok := ViewDirectColumn(expr); ok && SameViewRelation(column.Origin, source.Ref) {
				return nil
			}
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "WITH CHECK OPTION is supported only on automatically updatable views", Hint: reason}
}
