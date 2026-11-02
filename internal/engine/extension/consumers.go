package extension

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ConsumerInstaller describes ordinary SQL script products, never callable
// provenance. Installation and native replay consume this one declaration list.
type ConsumerInstaller interface {
	ConsumerViews(version string) ([]*parser.CreateView, error)
}
type consumerStage interface {
	Stage
	ScriptView(authorization.PrincipalRef, ids.ObjectAddress, *parser.CreateView) (*catalog.Relation, error)
}

func StageConsumers(ctx context.Context, stage Stage, implementation catalog.ExtensionImplementation, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	provider, ok := implementation.(ConsumerInstaller)
	if !ok {
		return nil
	}
	declarations, err := provider.ConsumerViews(ext.Version)
	if err != nil {
		return err
	}
	target, ok := stage.(consumerStage)
	if !ok {
		return fmt.Errorf("stuntgres: ordinary script consumers lack staging authority")
	}
	for _, declaration := range declarations {
		if err := ctx.Err(); err != nil {
			return err
		}
		relation, err := target.ScriptView(owner, ext.TargetNamespace, declaration)
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		member, err := catalog.WithExtensionMembership(relation, ext.Address(), revision)
		if err != nil {
			return err
		}
		next := member.(*catalog.Relation)
		baseline := next.Security.ACL
		if !baseline.Explicit {
			baseline = authorization.DefaultBaseline(owner, authorization.DefaultTables, false)
		}
		next.Security.ACL, _, err = authorization.ChangeObjectACL(ctx, stage.Roles(), baseline, owner, owner, []authorization.ACLGrantee{{Public: true}}, authorization.SelectPrivilege, false, false, false, false)
		if err != nil {
			return err
		}
		if err := stage.Replace(next); err != nil {
			return err
		}
		stage.RecordInitialPrivilege(next.Address(), 'e', next.Security.ACL)
		ext.Members = append(ext.Members, next.Address())
	}
	return stage.Replace(ext)
}
