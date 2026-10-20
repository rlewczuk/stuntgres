package engine

import "stuntgres/internal/engine/session"

// Temporary aliases for the session-layer types and constants that moved into
// session (ENGINE-SPLIT steps 13d.1 and 13d.6). Step 13e removes them.
type (
	catalogChanges               = session.Journal
	catalogDependencyObservation = session.DependencyObservation
	catalogStorageChange         = session.StorageChange
	EndReason                    = session.EndReason
	Isolation                    = session.Isolation
	noticeLevel                  = session.NoticeLevel
	Notification                 = session.Notification
	notificationAction           = session.NotificationAction
	Operation                    = session.Operation
	ProtocolCycle                = session.ProtocolCycle
	randomState                  = session.RandomState
	ResourceHandle               = session.ResourceHandle
	ResourceKind                 = session.ResourceKind
	savepointMark                = session.SavepointMark
	Session                      = session.Session
	sessionAuthorization         = session.AuthorizationState
	SessionResource              = session.SessionResource
	sessionSettings              = session.SettingsStore
	SessionStart                 = session.SessionStart
	settingsMark                 = session.SettingsMark
	StatementLease               = session.StatementLease
	transactionOwner             = session.TransactionOwner
	TxOptions                    = session.TxOptions
	TxState                      = session.TxState
)

const (
	CleanClose        = session.CleanClose
	InitFailure       = session.InitFailure
	InvalidConnection = session.InvalidConnection
	Terminated        = session.Terminated
	Abandoned         = session.Abandoned
	Settings          = session.Settings
	Temporary         = session.Temporary
	PreparedSQL       = session.PreparedSQL
	Cursors           = session.Cursors
	Authorization     = session.Authorization
	Transaction       = session.Transaction
	Notifications     = session.Notifications
	AdvisorySession   = session.AdvisorySession
	TxIdle            = session.TxIdle
	TxImplicit        = session.TxImplicit
	TxActive          = session.TxActive
	TxAborted         = session.TxAborted
	DefaultIsolation  = session.DefaultIsolation
	ReadCommitted     = session.ReadCommitted
	ReadUncommitted   = session.ReadUncommitted
	RepeatableRead    = session.RepeatableRead
	Serializable      = session.Serializable
)
