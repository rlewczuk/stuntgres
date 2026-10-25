package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

// binderOperation and binderEngine read a binder's host state in tests.
func binderOperation(b *analyze.Binder) *session.Operation { return b.Host.(*core.BindingHost).Op }
