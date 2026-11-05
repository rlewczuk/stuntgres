package core

// AttachedDatabaseName captures the runtime name under cluster metadata. Live
// sessions instead retain their immutable admission-time DatabaseName.
func (e *Engine) AttachedDatabaseName() string {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.DatabaseName
}
