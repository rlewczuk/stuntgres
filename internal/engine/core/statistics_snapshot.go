package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StatisticsSnapshotCodec borrows the captured catalog and ordinary control
// constructor authority. The caller owns the engine lock; no live SQL executes.
func StatisticsSnapshotCodec(ctx context.Context, e *Engine, read catalog.Read) storage.StatisticsSnapshotCodec {
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, read)
	principal := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	auth := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: read, Catalog: read, Roles: roles, RoleRecord: e.RoleStore.Published, State: authorization.RoleState{CurrentUser: principal, Authenticated: principal}})
	acl := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: roles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(regobj.Float4DumpContext(ctx), acl)
	return statisticsSnapshotCodec{ctx: ctx, engine: e, read: read, auth: auth}
}

type statisticsSnapshotCodec struct {
	ctx    context.Context
	engine *Engine
	read   catalog.Read
	auth   AuthorizationView
}

func (c statisticsSnapshotCodec) Expression(owner *catalog.Relation, node expr.Expr) (string, error) {
	return (stored.Deparser{View: c.read, Relation: owner}).SQL(c.ctx, node, stored.DeparseParent{})
}

func (c statisticsSnapshotCodec) Encode(usage sqltype.Usage, value sqltype.Value) (json.RawMessage, error) {
	node, err := table.CaptureStatisticsReplayNode(c.ctx, c.read, usage, value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(node)
}

func (c statisticsSnapshotCodec) Decode(usage sqltype.Usage, data json.RawMessage) (sqltype.Value, error) {
	var node typedef.DomainReplayNode
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&node); err != nil {
		return sqltype.Value{}, err
	}
	local := exec.Context{EvalStatement: exec.EvalStatement{Context: c.ctx, Catalog: c.read}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &c.auth}}
	local.Input.ACLItem = regobj.ACLItemInput{Roles: c.auth.Roles, Authority: c.read.Owner().ACLAuthority}
	local.Input.DomainChecks = &local
	env := typedef.DomainReplayEnv{Context: c.ctx, Catalog: c.read, Input: local.Input, Output: local.Output, Scalar: c.scalar, ValidateType: (stored.TypeProvider{}).Validate}
	env.StatisticsTree = sqltype.StatisticsRetainedTreeValue
	if _, err := decoder.Token(); err != io.EOF {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics value constructor")
	}
	value, _, err := typedef.RestoreDomainReplayNode(&env, usage, &node)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() || value.Type() != usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics value usage")
	}
	canonical, err := c.Encode(usage, value)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !bytes.Equal(canonical, data) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: noncanonical statistics value constructor")
	}
	return value, nil
}

func (c statisticsSnapshotCodec) scalar(raw parser.Expr, usage sqltype.Usage) (sqltype.Value, error) {
	if err := view.MaterializedSnapshotScalar(raw); err != nil {
		return sqltype.Value{}, err
	}
	input := sqltype.InputEnvironment{ACLItem: regobj.ACLItemInput{Roles: c.auth.Roles, Authority: c.read.Owner().ACLAuthority}}
	host := statisticsSnapshotBindingHost{BindingHost: c.engine.AnalysisHost(nil), context: c.ctx, auth: c.auth}
	binder := analyze.NewBinder(c.ctx, &host, input, analyze.Expected{})
	binder.Query.Auth = &c.auth
	binder.Query.Namespace = &analyze.QueryNamespace{}
	binder.Params.NoParameters = true
	node, err := binder.BindDefault(raw, catalog.Column{Name: "native statistics value", Usage: usage})
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := binder.CaptureRetainedWriteInput([]expr.Expr{node}, nil, nil); err != nil {
		return sqltype.Value{}, err
	}
	local := exec.Context{EvalStatement: exec.EvalStatement{Context: c.ctx, Catalog: c.read, Input: binder.Input}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &c.auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
	local.Input.DomainChecks = &local
	node, _, err = exec.PreflightCoercionsWithPolicy(&local, node, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	return node.Eval(&local)
}
