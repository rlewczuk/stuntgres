package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

// SessionHost names sessionHost.
type SessionHost = sessionHost

// EngineApplySetting calls x.applySetting.
func EngineApplySetting(x *Engine, op *session.Operation, owner *session.SettingsStore, d settings.Definition, input *string, local bool) (string, error) {
	return x.applySetting(op.Context(), op, owner, d, input, session.SettingAssignment(local), "")
}

// EngineExecuteSetting calls x.executeSetting.
func EngineExecuteSetting(x *Engine, op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	return x.executeSetting(op.Context(), op, stmt, count, implicit)
}

// PayloadNotifications returns x.notifications.
func PayloadNotifications(x *sessionPayload) session.NotificationListener { return x.notifications }

// PayloadNotificationsRef returns &x.notifications.
func PayloadNotificationsRef(x *sessionPayload) *session.NotificationListener {
	return &x.notifications
}

// PayloadSQLRoutines returns x.sqlRoutines.
func PayloadSQLRoutines(x *sessionPayload) map[ids.ObjectAddress]sqlRoutineAnalysis {
	return x.sqlRoutines
}

// EngineValidateOperation calls x.validateOperation.
func EngineValidateOperation(x *Engine, op *session.Operation) error {
	return x.validateOperation(op)
}

// EngineDatabaseAdmissionCount observes the transient physical reservation ledger.
func EngineDatabaseAdmissionCount(e *Engine) int {
	e.Cluster.AdmissionMu.Lock()
	defer e.Cluster.AdmissionMu.Unlock()
	return e.Cluster.admissions[e]
}

// DatabaseAdmissionSession observes the physical owner before startup publishes it.
func DatabaseAdmissionSession(a *DatabaseAdmission) *session.Session {
	a.cluster.AdmissionMu.Lock()
	defer a.cluster.AdmissionMu.Unlock()
	return a.owner
}

// DatabaseAdmissionSessionID observes the exact reserved physical identity.
func DatabaseAdmissionSessionID(a *DatabaseAdmission) ids.SessionID { return a.token.ID() }
