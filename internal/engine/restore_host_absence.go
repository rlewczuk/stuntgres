package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

type hostAbsenceState struct {
	record   host.PairAbsences
	end      int
	boundary *parser.SessionAuthorization
	applied  bool
}

func decodeRestoreHostAbsences(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := host.DecodePairAbsences(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	if r.hostAbsences != nil {
		return true, fmt.Errorf("stuntgres: duplicate host pair absence boundary")
	}
	r.hostAbsences = &hostAbsenceState{record: record, end: position.Offset + len(comment)}
	return true, nil
}
func pairRestoreHostAbsences(r *restoreRun) error {
	record := r.hostAbsences
	if len(r.hostRequirements) == 0 {
		if record != nil {
			return fmt.Errorf("stuntgres: orphan host pair absence boundary")
		}
		return nil
	}
	if record == nil || record.end < 0 || record.end > len(r.source) {
		return fmt.Errorf("stuntgres: missing host pair absence boundary")
	}
	// SessionAuthorization has no source position. Reuse the ordinary parser on
	// this complete prefix to pair the one native comment to its exact next AST;
	// no alternate SQL scanner, binder or authority is introduced.
	prefix, err := parser.ParseWithOptions(r.source[:record.end], parser.Options{Context: r.hostContext, StandardConformingStrings: true, StatisticsOwnerReplay: true})
	if err != nil {
		return err
	}
	index := len(prefix)
	if index >= len(r.statements) || !strings.HasPrefix(strings.TrimLeft(r.source[record.end:], " \t\r\n"), "SET SESSION AUTHORIZATION 'postgres';") {
		return fmt.Errorf("stuntgres: orphan host pair absence boundary")
	}
	directive, ok := r.statements[index].(*parser.SessionAuthorization)
	if !ok || directive.Name != "postgres" || directive.Default || !directive.OwnerDirective {
		return fmt.Errorf("stuntgres: invalid host pair absence authority boundary")
	}
	record.boundary = directive
	for _, absence := range record.record.Pairs {
		if !r.hostRequirements[absence.Provider] {
			return fmt.Errorf("stuntgres: host pair absence lacks provider prerequisite")
		}
		found := false
		for _, runtime := range r.stage.Catalog.HostRuntimes {
			if runtime.Provider() == absence.Provider && runtime.ABI() == absence.ABI {
				for _, conversion := range runtime.Definition().Conversions {
					found = found || conversion.Bootstrap && conversion.Type == absence.Type
				}
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: incompatible host pair absence capability")
		}
	}
	return nil
}
func afterRestoreHostAbsences(r *restoreRun, op *session.Operation, statement parser.Statement, _ control.Outcome) error {
	record := r.hostAbsences
	if record == nil || record.boundary != statement {
		return nil
	}
	if record.applied {
		return fmt.Errorf("stuntgres: repeated host pair absence boundary")
	}
	e := r.stage
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil || !actor.Superuser {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: host pair absence requires current administrator")
	}
	read := e.Catalog.Current()
	stage := e.BeginCatalogStage(read)
	changed := false
	for _, absence := range record.record.Pairs {
		var runtime *host.Runtime
		for _, candidate := range e.Catalog.HostRuntimes {
			if candidate.Provider() == absence.Provider && candidate.ABI() == absence.ABI {
				runtime = candidate
				break
			}
		}
		language, _ := read.Get(read.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: absence.Language})).(*catalog.Language)
		namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: absence.SQLName.Schema})
		typ, ok := read.Type(read.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: absence.SQLName.Name}))
		if runtime == nil || language == nil || language.Host != runtime || !ok || catalog.HostLogicalType(typ.SQLType) != absence.Type || (catalog.LanguageProvider{}).Validate(read, language) != nil {
			err = fmt.Errorf("stuntgres: unresolved host pair absence identity")
			break
		}
		if failure := (stored.TypeProvider{}).Validate(read, typ); failure != nil {
			err = failure
			break
		}
		key := ids.NameKey{ClassID: ids.TransformClassID, Name: fmt.Sprintf("%d,%d", typ.Address().ObjectID, language.Address().ObjectID)}
		pair, _ := read.Get(read.NameAddress(key)).(*catalog.Transform)
		if pair == nil {
			continue
		} // A preceding genuine directional DROP already retired it.
		if pair.Runtime != runtime || pair.SQLType != typ.SQLType {
			err = fmt.Errorf("stuntgres: incompatible host pair absence generation")
			break
		}
		for _, descriptor := range read.Descriptors() {
			if procedure, ok := descriptor.(*catalog.Procedure); ok && catalog.ProcedureDefinitionDependsOn(read, procedure, pair.Address()) {
				err = fmt.Errorf("stuntgres: host pair absence follows a dependent declaration")
				break
			}
		}
		if err != nil {
			break
		}
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(read.Epoch(), pair.Address()), false); err != nil {
			break
		}
		changed = true
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	if changed {
		prepared, err := e.ValidateCatalogStage(op.Context(), stage)
		if err != nil {
			return err
		}
		if _, err = e.PublishPreparedCatalog(op, nil, prepared, core.PublicationToken{Epoch: read.Epoch()}); err != nil {
			return err
		}
	}
	record.applied = true
	// This is an administrative native metadata boundary, not an ordinary
	// owner-constructor pair for the following user declaration.
	r.createOwner = authorization.PrincipalRef{}
	return nil
}
