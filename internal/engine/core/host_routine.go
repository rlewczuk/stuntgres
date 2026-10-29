package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// invokeHostRoutine borrows the ordinary frame/environment/statement. It owns no
// transaction, connection, mutable callback registry or alternate authority.
func (s *StatementScope) invokeHostRoutine(d *catalog.Procedure, frame *proc.Frame, result sqltype.Usage, admission AuthorizationView) (value sqltype.Value, err error) {
	ctx := s.executionContext()
	var inline [4]host.SQLValue
	inputs := inline[:min(len(frame.Parameters), len(inline))]
	if len(frame.Parameters) > len(inline) {
		inputs = make([]host.SQLValue, len(frame.Parameters))
	}
	defer clear(inputs)
	for i, v := range frame.Parameters {
		typ := catalog.HostLogicalType(v.Type())
		inputs[i] = host.SQLValue{Type: typ, Null: v.IsNull()}
		if v.IsNull() {
			continue
		}
		switch typ {
		case host.Bool:
			inputs[i].Bool, err = v.Boolean()
		case host.Int4, host.Int8:
			inputs[i].Int64, err = v.Integer()
		case host.Text:
			inputs[i].Text, err = v.Text()
		case host.Hstore:
			inputs[i].Hstore, err = v.OwnedHstoreEntries(ctx, frame.Inputs[i].Type)
		default:
			err = fmt.Errorf("stuntgres: unsupported host input type")
		}
		if err != nil {
			return value, err
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	before, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return value, err
	}
	if _, err = exec.ValidateRoutineDescriptor(before.Catalog, before.Roles, d); err != nil {
		return value, err
	}
	store := s.Op.EffectiveSettings()
	// Release the semantic catalog reservation as well as the mutex. Each kind
	// of application callback has its own balanced readmission, including the
	// supplied program called inside the handler.
	readmit := func() error {
		store.Mu.Lock()
		s.Op.Session.Mu.Lock()
		admissionErr := store.AdmitLocked(DomainOf(e), s.Op)
		s.Op.Session.Mu.Unlock()
		store.Mu.Unlock()
		if admissionErr != nil {
			return admissionErr
		}
		if failure := ctx.Err(); failure != nil {
			return failure
		}
		if ActiveScopeOfLease(s.Op.Lease) != s || s.Op.EffectiveSettings() != store || before.Catalog.Epoch() != e.Catalog.State.Epoch() {
			return ids.ErrCatalogReplaced
		}
		current, failure := s.RoutineAuthorizationLocked()
		if failure != nil {
			return failure
		}
		if _, failure = exec.ValidateRoutineDescriptor(current.Catalog, current.Roles, d); failure != nil {
			return failure
		}
		invoker := current
		invoker.State.CurrentUser = admission.State.CurrentUser
		if failure = invoker.Roles.ValidatePrincipal(invoker.State.CurrentUser); failure != nil {
			return failure
		}
		if failure = invoker.Check(access.Requirement{Object: e.Catalog.Domain.Ref(current.Catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege}); failure != nil {
			return failure
		}
		if current.Catalog.State() != before.Catalog.State() || current.Catalog.Domain() != before.Catalog.Domain() || current.Catalog.Epoch() != before.Catalog.Epoch() || current.Revision != before.Revision || current.RoleRecord != before.RoleRecord || current.State != before.State {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	admit := func() error { e.Mu.Lock(); defer e.Mu.Unlock(); return readmit() }
	s.releaseCatalogReservation()
	e.Mu.Unlock()
	var output host.SQLValue
	func() {
		defer e.Mu.Lock()
		tokens := d.Host.Tokens
		output, err = d.Host.Program.Invoke(ctx, inputs, tokens[:len(inputs)], tokens[len(inputs)], admit)
	}()
	if admissionErr := readmit(); admissionErr != nil {
		return value, admissionErr
	}
	if err != nil {
		return value, err
	}
	if output.Type != catalog.HostLogicalType(result.Type) {
		return value, fmt.Errorf("stuntgres: host result has wrong SQL type")
	}
	// Only the selected tagged payload may be active, including for SQL NULL.
	payloads := 0
	if output.Bool {
		payloads++
	}
	if output.Int64 != 0 {
		payloads++
	}
	if output.Text != "" {
		payloads++
	}
	if output.Hstore != nil {
		payloads++
	}
	if output.Null {
		if payloads != 0 {
			return value, fmt.Errorf("stuntgres: contradictory host SQL NULL")
		}
		return sqltype.Null(result.Type), nil
	}
	switch output.Type {
	case host.Bool:
		if output.Int64 != 0 || output.Text != "" || output.Hstore != nil {
			break
		}
		return sqltype.Boolean(result.Type, output.Bool), nil
	case host.Int4, host.Int8:
		if output.Bool || output.Text != "" || output.Hstore != nil {
			break
		}
		return sqltype.Integer(result.Type, output.Int64)
	case host.Text:
		if output.Bool || output.Int64 != 0 || output.Hstore != nil {
			break
		}
		if err = sqltype.ValidateTextInput(output.Text); err != nil {
			return value, err
		}
		return sqltype.String(result.Type, output.Text), nil
	case host.Hstore:
		if output.Bool || output.Int64 != 0 || output.Text != "" || output.Hstore == nil {
			break
		}
		return result.Type.HstoreFromEntries(ctx, output.Hstore)
	}
	return value, fmt.Errorf("stuntgres: contradictory host SQL result payload")
}
