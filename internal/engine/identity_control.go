package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sequenceNumericSQL(options sequenceOptions) string {
	cycle:="NO CYCLE";if options.cycle { cycle="CYCLE" }
	return fmt.Sprintf("INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s",options.increment,options.min,options.max,options.start,options.cache,cycle)
}

func identityDumpDivergent(options sequenceOptions, column sqltype.ID) bool {
	lo,hi,_:=sequenceTypeLimits(column)
	return options.persistence!="logged" || options.typ!=column || options.min<lo || options.max>hi || options.start<lo || options.start>hi
}

// Restore admits only the parameter-free configuration of an existing internal
// sequence. It is reconstruction in the isolated engine, not ALTER history.
func (e *Engine) validateRestoreIdentityConfiguration(op *Operation, syntax *parser.AlterSequence) error {
	e.mu.Lock();defer e.mu.Unlock()
	auth,err:=e.authorizationLocked(op);if err!=nil { return err }
	ref,err:=e.resolveRelationReference(auth,syntax.Name,false);if err!=nil { return err }
	relation,ok:=auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || relation.sequence==nil || !relation.sequence.identity { return sqlError("0A000","restore accepts only generated sequence ownership or identity configuration") }
	if syntax.Action=="logged" || syntax.Action=="unlogged" { return nil }
	if syntax.Action!="" || len(syntax.Options)==0 { return sqlError("0A000","restore accepts only generated identity configuration") }
	for _,option:=range syntax.Options {
		switch option.Name { case "as","increment","min","max","start","cache","cycle","restart": default:return sqlError("0A000","restore accepts only generated identity configuration") }
	}
	return nil
}
