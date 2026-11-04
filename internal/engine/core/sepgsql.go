package core

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// InstallSepgsqlSimulation is prepublication INTERNAL bootstrap only. Native
// replacement activates the registration with install=false, never reinstalls
// dropped SQL declarations or overwrites imported explicit labels.
func (e *Engine) InstallSepgsqlSimulation(policy *sepgsql.Runtime, install bool) error {
	e.Sepgsql, e.Catalog.Sepgsql = policy, policy
	if policy == nil || !install || !policy.InstallSQL() {
		return nil
	}
	state := catalog.CloneState(e.Catalog.State)
	read := e.Catalog.Read(state)
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, symbol := range [...]string{"sepgsql_getcon", "sepgsql_setcon", "sepgsql_mcstrans_in", "sepgsql_mcstrans_out", "sepgsql_restorecon"} {
		signature, result, strict, _ := catalog.SepgsqlDeclaration(symbol)
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			return err
		}
		capability, err := catalog.NewSepgsqlRoutine(read, policy, symbol, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, nil)
		if err != nil {
			return err
		}
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: symbol, ObjectRevision: 1, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Volatile, Strict: strict, Parallel: 'u', Cost: 1, Language: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 13}, Sepgsql: capability, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/sepgsql", HasBinary: true}}
		if state.NameAddress(catalog.DescriptorNameKey(p)) != (ids.ObjectAddress{}) {
			return fmt.Errorf("stuntgres: sepgsql bootstrap declaration collision")
		}
		state.SetDescriptor(p.Address(), p)
		state.BindName(catalog.DescriptorNameKey(p), p.Address())
	}
	state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
	e.Catalog.State = state
	e.InitializeSepgsqlSeedLabels()
	return nil
}

// InitializeSepgsqlSeedLabels reconstructs only configured startup labels on a
// private, freshly composed bootstrap catalog. It never installs SQL routines.
// Restore and actual template copies must not call it over imported state.
func (e *Engine) InitializeSepgsqlSeedLabels() {
	policy := e.Sepgsql
	if policy == nil || !policy.InstallSQL() {
		return
	}
	read := e.Catalog.Current()
	var next *catalog.State
	for _, target := range sepgsqlTargets(read, false) {
		object := target
		object.SubID = 0
		d := read.Get(object)
		label, matched := policy.InitialLabel(nil, catalog.SepgsqlClass(d, target.SubID), catalog.SepgsqlObjectKey(read, d, target.SubID))
		if !matched {
			continue
		}
		if next == nil {
			next = catalog.CloneState(read.State())
		}
		next.SetSecurityLabel(catalog.SecurityLabelKey{Object: target, Provider: "selinux"}, &catalog.SecurityLabelRecord{Label: label, PolicyID: policy.ID(), PolicyVersion: policy.Version()})
	}
	if next != nil {
		e.Catalog.State = next
	}
}
func sepgsqlTargets(view catalog.Read, includePeerDatabases bool) []ids.ObjectAddress {
	var targets []ids.ObjectAddress
	for address, d := range view.Descriptors() {
		if catalog.SepgsqlClass(d, 0) == "" {
			continue
		}
		if _, database := d.(*catalog.Database); database && !includePeerDatabases && address != view.Owner().DatabaseAddress() {
			continue
		}
		targets = append(targets, address)
		if relation, ok := d.(*catalog.Relation); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.CatalogRelation && !relation.CatalogView) {
			for sub := range catalog.SepgsqlAttributes(relation) {
				column := address
				column.SubID = sub
				targets = append(targets, column)
			}
		}
	}
	slices.SortFunc(targets, func(a, b ids.ObjectAddress) int {
		if a.ClassID < b.ClassID {
			return -1
		}
		if a.ClassID > b.ClassID {
			return 1
		}
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		if a.SubID < b.SubID {
			return -1
		}
		if a.SubID > b.SubID {
			return 1
		}
		return 0
	})
	return targets
}
func StageSepgsqlRelabel(auth AuthorizationView, stage *CatalogStage, address ids.ObjectAddress, text string, remove bool) error {
	policy := stage.Owner.Sepgsql
	if policy == nil {
		return fmt.Errorf("stuntgres: missing sepgsql simulation")
	}
	target := text
	if remove {
		target = policy.Unlabeled()
	} else if !policy.Valid(text) {
		return SQLError("42602", fmt.Sprintf(`SELinux: invalid security label: "%s"`, text))
	}
	object := address
	object.SubID = 0
	d := auth.Catalog.Get(object)
	class := catalog.SepgsqlClass(d, address.SubID)
	if class == "" {
		return SQLError("0A000", "SELinux: security labels are not supported for this type of object")
	}
	key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
	stage.ObserveSecurityLabel(key)
	if err := auth.View.SepgsqlCheck(address, sepgsql.Setattr|sepgsql.RelabelFrom); err != nil {
		return err
	}
	if _, err := auth.View.SepgsqlDecision(target, class, catalog.SepgsqlObjectKey(auth.Catalog, d, address.SubID), sepgsql.RelabelTo, true); err != nil {
		return err
	}
	var record *catalog.SecurityLabelRecord
	if !remove {
		record = &catalog.SecurityLabelRecord{Label: text, PolicyID: policy.ID(), PolicyVersion: policy.Version()}
	}
	stage.RecordSecurityLabel(key, record)
	return nil
}
func (s *StatementScope) invokeSepgsql(d *catalog.Procedure, frame *proc.Frame, result sqltype.Usage) (sqltype.Value, error) {
	e, op := s.Engine(), s.Op
	policy := e.Sepgsql
	if policy == nil || d.Sepgsql == nil || d.Sepgsql.Policy() != policy || op.Session.Sepgsql == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing sepgsql invocation authority")
	}
	if err := op.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	switch d.Sepgsql.Symbol() {
	case "sepgsql_getcon":
		return sqltype.String(result.Type, op.Session.Sepgsql.Current()), nil
	case "sepgsql_mcstrans_in", "sepgsql_mcstrans_out":
		argument := frame.Parameters[0]
		if argument.IsNull() {
			return sqltype.Null(result.Type), nil
		}
		text, err := argument.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(result.Type, policy.Translate(text, d.Sepgsql.Symbol() == "sepgsql_mcstrans_in")), nil
	case "sepgsql_setcon":
		target := op.Session.Sepgsql.Original()
		if !frame.Parameters[0].IsNull() {
			var err error
			target, err = frame.Parameters[0].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			if !policy.Valid(target) {
				return sqltype.Value{}, SQLError("42602", fmt.Sprintf(`SELinux: invalid security label: "%s"`, target))
			}
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			_, err = auth.View.SepgsqlDecision(auth.Sepgsql.Subject, "process", "", sepgsql.Setcurrent, true)
		}
		if err == nil {
			_, err = auth.View.SepgsqlDecision(target, "process", "", sepgsql.Dyntransition, true)
		}
		if err == nil {
			err = op.Context().Err()
		}
		if err == nil {
			op.Session.Sepgsql.Set(target)
		}
		e.Mu.Unlock()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(result.Type, true), nil
	case "sepgsql_restorecon":
		var profile *string
		if !frame.Parameters[0].IsNull() {
			text, err := frame.Parameters[0].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			profile = &text
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			actor, failure := auth.Roles.Descriptor(auth.State.CurrentUser)
			err = failure
			if err == nil && !actor.Superuser {
				err = SQLError("42501", "SELinux: must be superuser to restore initial contexts")
			}
		}
		if err == nil && !policy.HasSpec(profile) {
			err = SQLError("XX000", "SELinux: failed to initialize labeling handle: No such file or directory")
		}
		var stage *CatalogStage
		if err == nil {
			stage = e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, op.Session)
			targets := sepgsqlTargets(auth.Catalog, true)
			// Released restorecon visits shared databases before local catalogs.
			// Both passes retain the same actual targets and one atomic stage.
			for _, databases := range [...]bool{true, false} {
				for _, target := range targets {
					if (target.ClassID == ids.DatabaseClassID) != databases {
						continue
					}
					if err = op.Context().Err(); err != nil {
						break
					}
					object := target
					object.SubID = 0
					descriptor := auth.Catalog.Get(object)
					class := catalog.SepgsqlClass(descriptor, target.SubID)
					key := catalog.SepgsqlObjectKey(auth.Catalog, descriptor, target.SubID)
					label, matched := policy.InitialLabel(profile, class, key)
					if !matched {
						op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf("SELinux: no initial label assigned for %s (type=%d), skipping", key, sepgsql.ClassNumber(class))})
						continue
					}
					if err = StageSepgsqlRelabel(auth, stage, target, label, false); err != nil {
						break
					}
				}
				if err != nil {
					break
				}
			}
		}
		e.Mu.Unlock()
		if stage != nil {
			defer e.DiscardCatalogStage(stage)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		prepared, err := e.ValidateCatalogStage(op.Context(), stage)
		if err != nil {
			return sqltype.Value{}, err
		}
		_, err = e.PublishCatalogOperation(op, s, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(result.Type, true), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unknown sepgsql declaration capability")
}

// enterSepgsqlRoutine checks a trusted entry without SQL-user substitution.
func (s *StatementScope) enterSepgsqlRoutine(d *catalog.Procedure) (func(), error) {
	token, err := s.enterSepgsqlBody(d, false, nil)
	if err != nil || !token.Active() {
		return nil, err
	}
	return token.Leave, nil
}
func (h *statementHost) EnterNativeRoutine(d *catalog.Procedure) (sqltype.RoutineBodyToken, error) {
	// Actual body entry is separate from serial expression admission.
	return h.scope().enterSepgsqlBody(d, false, nil)
}
func (h *statementHost) EnterNativeAggregateSupport(aggregate, support *catalog.Procedure) (sqltype.RoutineBodyToken, error) {
	if aggregate == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing aggregate support owner")
	}
	return h.scope().enterSepgsqlBody(support, false, aggregate)
}
func (s *StatementScope) enterSepgsqlBody(d *catalog.Procedure, execute bool, aggregate *catalog.Procedure) (sqltype.RoutineBodyToken, error) {
	e, op := s.Engine(), s.Op
	if e.Sepgsql == nil || op.Session.Sepgsql == nil || e.ImportTargets != nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	target, transition := "", false
	if err == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, d, execute, aggregate)
	}
	e.Mu.Unlock()
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, nil
	}
	return op.Session.Sepgsql.Enter(target), nil
}

// sepgsqlBodyDecision uses only a caller-authenticated current view while Mu is
// held. Ordinary and genuine candidate dictionary owners share this decision,
// but only those owners may choose the view. No kernel executes under Mu.
func (s *StatementScope) sepgsqlBodyDecision(auth AuthorizationView, d *catalog.Procedure, execute bool, aggregate *catalog.Procedure) (string, bool, error) {
	var err error
	if aggregate != nil {
		_, d, err = exec.ValidateAggregateSupportMembership(auth.Catalog, auth.Roles, aggregate, d)
	} else {
		d, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
	}
	if err != nil {
		return "", false, err
	}
	if execute {
		if err = auth.View.SepgsqlCheck(d.Address(), sepgsql.Permission("db_procedure", "execute")); err != nil {
			return "", false, err
		}
	}
	if routine.FmgrBuiltin(d) {
		return "", false, nil
	}
	if err = auth.View.SepgsqlLabelBasis(d.Address()); err != nil {
		return "", false, err
	}
	target, transition := s.Engine().Sepgsql.TrustedContext(auth.Sepgsql.Subject, auth.View.SepgsqlContext(d.Address()))
	if target == auth.Sepgsql.Subject {
		transition = false
	}
	if transition {
		err = auth.View.SepgsqlCheck(d.Address(), sepgsql.Permission("db_procedure", "entrypoint"))
		if err == nil {
			_, err = auth.View.SepgsqlDecision(target, "process", "", sepgsql.Transition, true)
		}
	}
	return target, transition, err
}

var _ access.SepgsqlAuditSink = (*session.Operation)(nil)
