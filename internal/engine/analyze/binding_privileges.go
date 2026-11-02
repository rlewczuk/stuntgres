package analyze

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Privileges is the statement authority the binding walkers check against.
// The engine implements it over its AuthorizationView, which pairs the
// captured access.View with its session environment, so a principal switch
// moves the checks and the $user entry of the reference path together. The
// view-definition walkers stay with the engine's view payloads; the walkers
// reach them through CheckViewWrite and CheckViewRowMarks.
type Privileges interface {
	Check(req access.Requirement) error
	CheckColumns(ref ids.ObjectRef, relation *catalog.Relation, mask authorization.Privileges, indices []int) error
	// WithCurrentUser returns the authority running as principal, and the
	// principal's validation error, if any.
	WithCurrentUser(principal authorization.PrincipalRef) (Privileges, error)
	// CheckViewWrite checks a write through an auto-updatable view
	// (b.ViewWrite != nil): the logical target, then each layer.
	CheckViewWrite(b Binding, stmt parser.Statement) error
	// CheckViewRowMarks checks a view source's stored and inherited row marks
	// with the current user as invoker; it checks nothing when none apply.
	CheckViewRowMarks(relation *catalog.Relation, mark plan.RowMark) error
}

// CheckBinding checks a bound statement's privileges. A write through a view
// goes to auth.CheckViewWrite; otherwise the target's command privileges and
// column reads come first, then the query sources. auth decides each object.
func CheckBinding(auth Privileges, b Binding, stmt parser.Statement) error {
	if b.ViewWrite != nil {
		return auth.CheckViewWrite(b, stmt)
	}
	switch stmt.(type) {
	case *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable:
		return CheckQuerySources(auth, b.QuerySources)
	}
	if b.Relation == nil {
		return nil
	}
	if err := BindingRequirements(b, stmt, func(req Requirement) error { return req.Check(auth) }); err != nil {
		return err
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Merge, *parser.Delete, *parser.Update:
		return CheckQuerySources(auth, b.QuerySources)
	}
	return nil
}

// CheckQuerySources checks the SELECT columns of every executable source, then
// the row-mark UPDATE rights and view row marks of every source, each under the
// source's own principal when it has one (a definer view's owner).
func CheckQuerySources(auth Privileges, sources []exec.Leaf) error {
	for _, source := range sources {
		if source.AnalysisOnly {
			continue
		}
		sourceAuth := auth
		if source.Principal != (authorization.PrincipalRef{}) {
			var err error
			if sourceAuth, err = auth.WithCurrentUser(source.Principal); err != nil {
				return err
			}
		}
		if err := SourceReadRequirements(&source, func(req Requirement) error { return req.Check(sourceAuth) }); err != nil {
			return err
		}
	}
	for _, source := range sources {
		sourceAuth := auth
		if source.Principal != (authorization.PrincipalRef{}) {
			var err error
			if sourceAuth, err = auth.WithCurrentUser(source.Principal); err != nil {
				return err
			}
		}
		if err := SourceRowMarkRequirements(&source, func(req Requirement) error { return req.Check(sourceAuth) }); err != nil {
			return err
		}
		if source.Relation.RelationKind == catalog.ViewRelation {
			if err := sourceAuth.CheckViewRowMarks(source.Relation, source.Mark); err != nil {
				return err
			}
		}
	}
	return nil
}
