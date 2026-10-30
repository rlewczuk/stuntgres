package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
)

// CheckCreationTablespace consumes admitted execution policy while Engine.Mu
// is held. It never takes a settings lock or recaptures operation authority.
func (h DDLHost) CheckCreationTablespace(a ddl.Authority, persistence string, partitioned bool) error {
	auth := DDLAuthorityOf(a)
	if persistence != "temporary" || h.Op == nil {
		return ddl.CheckDefaultTablespaceCreation(auth.View, auth, h.DefaultTablespace(), persistence, partitioned)
	}
	return h.checkTempTablespaceCreation(auth)
}

func (h DDLHost) checkTempTablespaceCreation(auth AuthorizationView) error {
	op := h.Op
	t := op.Session.Transactions
	selector := &t.TempTablespaces
	if !selector.Current(t.Ref, op.TempTablespaceAssignment) {
		spaces, accepted := op.AcceptedTempTablespaces(t.Ref)
		if !accepted {
			var err error
			spaces, err = ddl.PrepareTempTablespaces(auth.View, auth, op.TempTablespaces, false)
			if err != nil {
				return err
			}
		}
		selector.Install(t.Ref, op.TempTablespaceAssignment, spaces)
	}
	return ddl.CheckTempTablespaceCreation(auth.View, auth, selector.Next())
}

// CheckClonedIndexTablespaceCreation keeps PostgreSQL's check_rights=false
// permanent-index branch separate. A TEMP clone still consumes TEMP policy.
// The caller supplies both coherent authority and its stage catalog read.
func (h DDLHost) CheckClonedIndexTablespaceCreation(a ddl.Authority, read catalog.Read, persistence string, partitioned bool) error {
	if persistence != "temporary" || h.Op == nil {
		return ddl.CheckClonedIndexTablespaceCreation(read, h.DefaultTablespace(), persistence, partitioned)
	}
	auth := DDLAuthorityOf(a)
	auth.Recapture(read)
	return h.checkTempTablespaceCreation(auth)
}
