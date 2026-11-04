package exec

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func indexCallbackSite(callback catalog.IndexRoutineCallback) DirectRoutineInitializationSite {
	return DirectRoutineInitializationSite{Routine: callback.Helper, Reference: callback.Reference, Purpose: DirectIndexSupport, Source: callback.Source, Ordinal: callback.Slot}
}
func InitializeIndexSupport(c *Context, support sqltype.IndexSupport, slot uint16) error {
	if c == nil {
		return fmt.Errorf("stuntgres: missing index support source owner")
	}
	if owner := c.Catalog.Owner(); owner == nil || owner.Sepgsql == nil {
		return nil
	}
	callback, err := catalog.CaptureIndexRoutineCallback(c.Catalog, support, slot)
	if err != nil {
		return err
	}
	if routine.FmgrBuiltin(callback.Helper) {
		return nil
	}
	return BeginDirectRoutineInitialization(c, indexCallbackSite(callback))
}
func (c *Context) EnterIndexSupport(support sqltype.IndexSupport, slot uint16) (sqltype.RoutineBodyToken, error) {
	if c == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing actual index support owner")
	}
	active, err := routineSimulationActive(c)
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if !active {
		return sqltype.RoutineBodyToken{}, nil
	}
	callback, err := catalog.CaptureIndexRoutineCallback(c.Catalog, support, slot)
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if routine.FmgrBuiltin(callback.Helper) {
		return sqltype.RoutineBodyToken{}, nil
	}
	if c.ClosedEvaluation != nil {
		return c.EnterInitializedDirectRoutine(indexCallbackSite(callback))
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing live index support owner")
	}
	return host.EnterIndexSupportCallback(callback)
}

// InternalIndexPublicationEvaluation is the existing no-SQL-principal
// publication lane, not permission obtained from a missing host. Only the
// explicit trusted publication owner calls it after selecting its immutable root.
func InternalIndexPublicationEvaluation(c *Context, view catalog.Read) (Context, error) {
	if c == nil || view.State() == nil || view.Owner() == nil || c.Host != nil || c.Query != nil || c.ClosedEvaluation != nil || c.RoutineInitialization.Active() || c.routineSourceError != nil || c.clientRoutineAuthority() {
		return Context{}, fmt.Errorf("stuntgres: client borrowed internal index publication")
	}
	local := *c
	local.Catalog = view
	local.indexPublication = view
	return local, nil
}

func (c *Context) internalIndexPublication() bool {
	return c.indexPublication.State() != nil && c.indexPublication == c.Catalog && c.Host == nil && c.Query == nil && c.ClosedEvaluation == nil && !c.RoutineInitialization.Active() && c.routineSourceError == nil && !c.clientRoutineAuthority()
}

func (c *Context) clientRoutineAuthority() bool {
	if c.RoutineAuthority == nil {
		return false
	}
	auth := c.RoutineAuthority.AdmissionAccess()
	return auth.Sepgsql.Policy != nil || auth.coordinatorCaptured
}
