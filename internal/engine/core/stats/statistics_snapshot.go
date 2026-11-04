package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"stuntgres/internal/engine/analyze"
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

// SnapshotCodec is the storage.StatisticsSnapshotCodec over a captured
// catalog and its host's control constructor authority. The caller owns the
// engine lock; no live SQL executes.
type SnapshotCodec struct {
	ctx  context.Context
	read catalog.Read
	host SnapshotHost
}

var _ storage.StatisticsSnapshotCodec = SnapshotCodec{}

// NewSnapshotCodec borrows read and host; ctx carries the control-literal
// output context.
func NewSnapshotCodec(ctx context.Context, read catalog.Read, host SnapshotHost) SnapshotCodec {
	return SnapshotCodec{ctx: ctx, read: read, host: host}
}

func (c SnapshotCodec) Expression(owner *catalog.Relation, node expr.Expr) (string, error) {
	return (stored.Deparser{View: c.read, Relation: owner}).SQL(c.ctx, node, stored.DeparseParent{})
}

func (c SnapshotCodec) Encode(usage sqltype.Usage, value sqltype.Value) (json.RawMessage, error) {
	node, err := table.CaptureStatisticsReplayNode(c.ctx, c.read, usage, value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(node)
}

func (c SnapshotCodec) Decode(usage sqltype.Usage, data json.RawMessage) (sqltype.Value, error) {
	var node typedef.DomainReplayNode
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&node); err != nil {
		return sqltype.Value{}, err
	}
	auth := c.host.Authority()
	local := exec.Context{EvalStatement: exec.EvalStatement{Context: c.ctx, Catalog: c.read}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: auth}}
	local.ControlLiteralAuthority = c.host.ControlLiteralAuthority()
	local.Input.ACLItem = regobj.ACLItemInput{Roles: auth.Access().Roles, Authority: c.read.Owner().ACLAuthority}
	local.Input.DomainChecks = &local
	local.Input = local.FunctionInput()
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

func (c SnapshotCodec) scalar(raw parser.Expr, usage sqltype.Usage) (sqltype.Value, error) {
	if err := view.MaterializedSnapshotScalar(raw); err != nil {
		return sqltype.Value{}, err
	}
	auth := c.host.Authority()
	local := exec.Context{EvalStatement: exec.EvalStatement{Context: c.ctx, Catalog: c.read}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: auth}}
	local.ControlLiteralAuthority = c.host.ControlLiteralAuthority()
	local.Input.ACLItem = regobj.ACLItemInput{Roles: auth.Access().Roles, Authority: c.read.Owner().ACLAuthority}
	binder := analyze.NewBinder(c.ctx, c.host.BindingHost(c.ctx), local.FunctionInput(), analyze.Expected{})
	binder.Query.Auth = auth
	binder.Query.Namespace = &analyze.QueryNamespace{}
	binder.Params.NoParameters = true
	node, err := binder.BindDefault(raw, catalog.Column{Name: "native statistics value", Usage: usage})
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := binder.CaptureRetainedWriteInput([]expr.Expr{node}, nil, nil); err != nil {
		return sqltype.Value{}, err
	}
	local.Input = binder.Input
	local.EvalFrame = exec.EvalFrame{Scope: 1, Frame: 1}
	local.RoutineRecipe, local.RoutineChecks = true, binder.Dependencies
	local.Input.DomainChecks = &local
	node, _, err = exec.PreflightCoercionsWithPolicy(&local, node, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	return node.Eval(&local)
}
