package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/nativetrig"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The native trigger bodies' host is the statement host; AuthorizationView is
// its authority. The text-search host, Context and LargeObject are shared
// with the other leaves and exec.Host. The authorization, sequence, identity
// and nested-SQL bodies stay core's; the trigger protocol checks and row work
// are the bodies'.
var _ nativetrig.Host[AuthorizationView] = (*statementHost)(nil)

// NamespaceAuthorizationLocked is the statement's namespace authorization:
// the callable authority over the statement's private catalog.
func (h *statementHost) NamespaceAuthorizationLocked() (AuthorizationView, error) {
	s := h.scope()
	return s.namespaceAuthorizationLocked(s.Engine())
}

// TextSearchInput resolves regconfig text in auth.
func (h *statementHost) TextSearchInput(auth AuthorizationView) regobj.TextSearchInput {
	return regobj.NewTextSearchInput(inputCapture(auth, nil))
}

// NextvalText is the legacy nextval(text): it resolves the relation name per
// demanded call. The captured ObjectRef then travels through the normal
// sequence gate and lock wait, never being retargeted by name or compensated
// on statement/transaction failure.
func (h *statementHost) NextvalText(text string) (int64, error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.namespaceAuthorizationLocked(e)
	var path []ids.ObjectAddress
	if err == nil {
		path, err = ReferencePath(auth)
	}
	e.Mu.Unlock()
	if err != nil {
		return 0, err
	}
	oid, err := regobj.NewClassInput(inputCapture(auth, path)).ResolveRelationName(s.Op.Context(), text)
	if err != nil {
		return 0, err
	}
	ref := auth.Ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	return h.Sequence(functions.SequenceRequest{Name: "nextval", Ref: ref, OID: ids.OID(oid), Temporary: auth.Session.SessionToken})
}

// CurrentUserValue is current_user as result, captured inside the routine
// environment the trigger body runs in.
func (h *statementHost) CurrentUserValue(result *sqltype.Descriptor) (sqltype.Value, error) {
	return (*sessionInquiryHost)(h).IdentityValue(&exec.Context{}, functions.IdentityCurrentUser, result)
}

// QueueNotification appends a validated event to the statement-local
// notification effects.
func (h *statementHost) QueueNotification(channel, payload string) {
	s := h.scope()
	s.notificationEvents = append(s.notificationEvents, session.Notification{PID: uint64(s.Op.Session.SessionToken.ID()), Channel: channel, Payload: payload})
}

// SetRoutineAtomic sets the PL atomic invocation barrier and returns the
// previous one for the caller to restore.
func (h *statementHost) SetRoutineAtomic(atomic bool) bool {
	s := h.scope()
	previous := s.plAtomic
	s.plAtomic = atomic
	return previous
}

// PrepareTriggerSQL admits and analyzes one generated statement without
// executing it: fresh lexical command scratch over read with the typed
// closed parameter inputs and all-row demand. It borrows lexical scratch
// only: no command completion or journal publication.
func (h *statementHost) PrepareTriggerSQL(statement parser.Statement, inputs []sqltype.Usage, read catalog.Read) (err error) {
	s := h.scope()
	restore := s.ActivateSQLRoutine(inputs, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.scalar, s.sqlRoutine.rows, s.sqlRoutine.closedParameters = false, true, true
	if err = s.AcquireRelation(statement, true); err != nil {
		return err
	}
	_, err = s.Engine().describeOperation(s, statement)
	return err
}

// ExecuteTriggerSQL runs one generated statement with parameters through the
// ordinary typed binder and executor and the outer journal, as one nested
// routine command: inner AFTER/constraint work completes before it returns.
// The count is the command's row count, or whether it returned a row.
func (h *statementHost) ExecuteTriggerSQL(statement parser.Statement, inputs []sqltype.Usage, read catalog.Read, parameters []sqltype.Value) (count int64, err error) {
	s := h.scope()
	restore := s.ActivateSQLRoutine(inputs, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.scalar, s.sqlRoutine.rows, s.sqlRoutine.closedParameters = false, true, true
	finish, err := s.BeginRoutineCommand()
	if err != nil {
		return 0, err
	}
	defer func() { err = finish(err) }()
	if err = s.AcquireRelation(statement, true); err != nil {
		return 0, err
	}
	description, err := s.Engine().describeOperation(s, statement)
	if err != nil {
		return 0, err
	}
	s.Expected = &description
	result, err := s.Engine().executeStatement(s, statement, len(parameters), func(_ context.Context, info analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != len(parameters) || len(dst) != len(parameters) {
			return fmt.Errorf("stuntgres: invalid refint parameter frame")
		}
		copy(dst, parameters)
		return nil
	})
	defer func() {
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); err == nil {
				err = closeErr
			}
		}
		for _, rows := range result.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	if err != nil {
		return 0, err
	}
	if result.Rows == nil {
		return result.Count, nil
	}
	row := make([]sqltype.Value, len(result.Rows.Schema()))
	present, err := result.Rows.Next(s.executionContext(), row)
	if err != nil {
		return 0, err
	}
	if present {
		return 1, nil
	}
	return 0, nil
}
