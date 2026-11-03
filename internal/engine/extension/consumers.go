package extension

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ConsumerInstaller describes ordinary SQL script products, never callable
// provenance. Installation and native replay consume this one declaration list.
type ConsumerInstaller interface {
	ConsumerViews(version string) ([]ConsumerView, error)
}

// ConsumerView is ordinary CREATE VIEW followed by its actual script ACL
// events. Nil ACL means no script grant/revoke, not implicit PUBLIC access.
type ConsumerView struct {
	Statement      *parser.CreateView
	ACL            []ConsumerViewACL
	InstallContext string
}

type ConsumerViewACL struct {
	Public     bool
	Role       uint64
	Privileges authorization.Privileges
	Revoke     bool
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
		relation, err := target.ScriptView(owner, ext.TargetNamespace, declaration.Statement)
		if err != nil {
			var diagnostic *sqlstate.Error
			if declaration.InstallContext != "" && errors.As(err, &diagnostic) {
				copy := *diagnostic
				copy.Context = declaration.InstallContext
				return &copy
			}
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
		if !baseline.Explicit && len(declaration.ACL) != 0 {
			baseline = authorization.DefaultBaseline(owner, authorization.DefaultTables, false)
		}
		for _, event := range declaration.ACL {
			grantee := authorization.ACLGrantee{Public: event.Public}
			if !event.Public {
				grantee.Role = stage.CurrentRead().Owner().Principals.Principal(event.Role)
				if _, err := stage.Roles().Descriptor(grantee.Role); err != nil {
					return err
				}
			}
			baseline, _, err = authorization.ChangeObjectACL(ctx, stage.Roles(), baseline, owner, owner, []authorization.ACLGrantee{grantee}, event.Privileges, false, event.Revoke, false, false)
			if err != nil {
				return err
			}
		}
		next.Security.ACL = baseline
		if err := stage.Replace(next); err != nil {
			return err
		}
		if len(declaration.ACL) != 0 {
			stage.RecordInitialPrivilege(next.Address(), 'e', next.Security.ACL)
		}
		ext.Members = append(ext.Members, next.Address())
	}
	return stage.Replace(ext)
}
