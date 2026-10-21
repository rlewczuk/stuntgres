package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// Validate only ACL leaves; the regular serializers retain every other lossless
// boundary. This runs under the existing capture lock before any writer call.
func preflightACLItemValue(ctx context.Context, output capturedACLItemOutput, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.Type() == nil || value.IsNull() || !value.Type().ContainsACLItem() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := preflightACLItemValue(ctx, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		if value.Type().BaseType().ID() != sqltype.ACLItemArray && containsEmptyACL(value) {
			return fmt.Errorf("stuntgres: dump cannot serialize rank-one empty ACL inside an array of composite carriers")
		}
		for i := range value.ArrayLen() {
			if err := preflightACLItemValue(ctx, output, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	authority, grantee, grantor, _, _, err := value.ACLItem()
	if err != nil {
		return err
	}
	if grantee != 0 {
		if _, err := output.FormatACLRole(authority, grantee); err != nil {
			return err
		}
	}
	_, err = output.FormatACLRole(authority, grantor)
	return err
}
