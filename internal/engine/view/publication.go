package view

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func PublicationTypeChangeError(v catalog.Read, target ids.ObjectAddress, column string, path []ids.ObjectAddress) error {
	for _, m := range v.PublicationRelations() {
		if m.Filter != nil && m.Filter.DependsOn(target) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used by a publication WHERE clause", Detail: stored.DependencyObject(v, m.Address(), path) + fmt.Sprintf(` depends on column "%s"`, column)}
		}
	}
	return nil
}
