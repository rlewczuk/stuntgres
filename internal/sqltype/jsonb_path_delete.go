package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/util"
)

func (p BinaryPlan) jsonbPathDelete(ctx context.Context, document, path Value) (Value, error) {
	if document.jsonb == nil || path.array == nil {
		return Value{}, fmt.Errorf("missing JSONB deletion payload")
	}
	if err := util.ExecutionCheckpoint(ctx, "jsonb.path_delete"); err != nil {
		return Value{}, err
	}
	root, changed, err := jsonbSetPath(ctx, document.jsonb.root, path, jsonbNode{}, false, jsonbDelete)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !changed {
		return document, nil
	}
	payload := &jsonbPayload{root: root}
	text, err := jsonbOutput(ctx, &payload.root)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text, jsonb: payload}, nil
}
