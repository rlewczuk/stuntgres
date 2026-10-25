package core

import (
	"sync"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// EngineDescribeOperation calls x.describeOperation.
func EngineDescribeOperation(x *Engine, scope *StatementScope, stmt parser.Statement) (StatementDescription, error) {
	return x.describeOperation(scope, stmt)
}

// EngineNotifications returns x.notifications.
func EngineNotifications(x *Engine) session.NotificationRegistry { return x.notifications }

// EngineSessionMuRef returns &x.sessionMu.
func EngineSessionMuRef(x *Engine) *sync.Mutex { return &x.sessionMu }

// EngineSessions returns x.sessions.
func EngineSessions(x *Engine) map[ids.SessionToken]*session.Session { return x.sessions }
