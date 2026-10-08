package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) identityCandidate(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old *relationDescriptor, action parser.AlterTableAction) (*relationDescriptor,error) {
	slot,ok:=old.index[action.Column.Text]
	if isSystemColumn(action.Column.Text) { return old,sqlError("0A000",fmt.Sprintf(`cannot alter system column "%s"`,action.Column.Text)) }
	if !ok { return old,sqlError("42703",fmt.Sprintf(`column "%s" of relation "%s" does not exist`,action.Column.Text,old.name)) }
	attribute:=old.attributes[slot]
	identity,exists:=old.identities[attribute]
	message:=fmt.Sprintf(`column "%s" of relation "%s"`,action.Column.Text,old.name)
	if action.Action==parser.AddIdentity {
		if exists { return old,sqlError("55000",message+" is already an identity column") }
		if old.columns[slot].Nullable { return old,sqlError("55000",message+" must be declared NOT NULL before identity can be added") }
		if _,ok:=old.defaults[attribute];ok { return old,sqlError("55000",message+" already has a default value") }
		next:=*old;next.identities=maps.Clone(old.identities)
		column:=&parser.Column{Name:action.Column.Text,Constraints:[]parser.ColumnConstraint{{Identity:action.Identity}}}
		e.mu.Lock()
		err:=e.stageCreateIdentities(auth,stage,&next,&parser.CreateTable{Elements:[]parser.TableElement{{Column:column}}})
		e.mu.Unlock()
		return &next,err
	}
	if !exists {
		if action.Action==parser.DropIdentity && action.IfExists {
			scope.op.emitNotice(levelNotice,sqlstate.Error{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:message+" is not an identity column, skipping"})
			return old,nil
		}
		return old,sqlError("55000",message+" is not an identity column")
	}
	ref:=ObjectRef{owner:e.owner,Epoch:stage.base.Epoch(),Address:identity.sequence}
	if action.Action==parser.DropIdentity {
		if err:=scope.acquireSequence(ref,accessExclusive,false);err!=nil { return old,err }
		e.mu.Lock()
		err:=e.refreshIdentitySequenceLocked(scope,stage,old,attribute,identity.sequence)
		if err==nil { stage.identityDependencies=append(stage.identityDependencies,catalogDependencyObservation{e.state,[]ObjectAddress{identity.sequence}}) }
		e.mu.Unlock()
		if err!=nil { return old,err }
		if err:=stageViewDependents(stage,[]ObjectAddress{identity.sequence},parser.DropRestrict);err!=nil { return old,err }
		if err:=stage.Remove(ref);err!=nil { return old,err }
		next:=*old;next.identities=maps.Clone(old.identities);delete(next.identities,attribute)
		return &next,nil
	}
	definition:=action.Identity
	next:=*old;next.identities=maps.Clone(old.identities)
	if definition.Mode!=parser.IdentityNone { identity.mode=definition.Mode;next.identities[attribute]=identity }
	if len(definition.Options)==0 { return &next,nil }
	for _,option:=range definition.Options {
		if option.Name=="logged" || option.Name=="unlogged" { return old,sqlError("XX000",fmt.Sprintf(`option "%s" not recognized`,option.Name)) }
		if option.Name=="as" || option.Name=="owned" { return old,expressionError("42601",fmt.Sprintf(`sequence option "%s" not supported here`,option.Name),option.Pos) }
	}
	if err:=scope.acquireSequence(ref,shareRowExclusive,false);err!=nil { return old,err }
	e.mu.Lock();defer e.mu.Unlock()
	if err:=e.refreshIdentitySequenceLocked(scope,stage,old,attribute,identity.sequence);err!=nil { return old,err }
	read:=e.stageRead(stage)
	sequence,ok:=read.state.descriptors[identity.sequence].(*relationDescriptor);if !ok { return old,ErrCatalogReplaced }
	generator:=stage.generators[identity.sequence];if generator==nil { generator=e.sequenceGeneratorLocked(scope,identity.sequence) }
	if generator==nil { return old,fmt.Errorf("stuntgres: sequence state missing") }
	options,restart,err:=bindSequenceOptions(sequence.sequence,*generator,definition.Options);if err!=nil { return old,err }
	revision,err:=e.revisionLocked();if err!=nil { return old,err }
	changed:=*sequence;changed.sequence=&options;changed.revision=revision;changed.sequencePlanStamp=revision
	if stage.base.state.descriptors[changed.address]==nil {
		stage.descriptors[changed.address]=&changed
	} else if err:=stage.Replace(&changed);err!=nil { return old,err }
	replacement:=*generator;replacement.generation=revision
	if restart!=nil { replacement.last=*restart;replacement.called=false }
	if stage.generators==nil { stage.generators=make(map[ObjectAddress]*sequenceGenerator) }
	stage.generators[identity.sequence]=&replacement
	return &next,nil
}

// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) refreshIdentitySequenceLocked(scope *StatementScope, stage *CatalogStage, table *relationDescriptor, attribute int32, address ObjectAddress) error {
	fresh,err:=e.authorizationLocked(scope.op);if err!=nil { return err }
	if fresh.catalog.Epoch()!=stage.base.Epoch() { return ErrCatalogReplaced }
	sequence,ok:=fresh.catalog.state.descriptors[address].(*relationDescriptor)
	if staged,exists:=stage.descriptors[address];exists { sequence,ok=staged.(*relationDescriptor) }
	owned:=table.address;owned.SubID=attribute
	if !ok || sequence.sequence==nil || !sequence.sequence.identity || sequence.sequence.owned!=owned { return ErrCatalogReplaced }
	if stage.identityOriginals==nil { stage.identityOriginals=make(map[ObjectAddress]Descriptor) }
	if _,captured:=stage.identityOriginals[address];!captured { stage.identityOriginals[address]=e.state.descriptors[address] }
	stage.base=fresh.catalog
	return nil
}
