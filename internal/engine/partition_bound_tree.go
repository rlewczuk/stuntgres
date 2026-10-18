package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
)

// Both membership and key slices belong to immutable descriptor roots. Capture
// these roots, not a live parent lookup, so eager output survives drop/replay.
// This native structural convention is intentionally not nodeToString or SQL.
type partitionBoundTree struct {
	membership *partitionMembership
	keys       []partitionKeyEntry
}

func (t partitionBoundTree) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.membership == nil {
		return "", fmt.Errorf("stuntgres: missing partition bound")
	}
	if t.membership.Strategy == parser.PartitionHash {
		return fmt.Sprintf("{PartitionBound strategy hash default false modulus %d remainder %d}", t.membership.Modulus, t.membership.Remainder), nil
	}
	if t.membership.DefaultBound {
		return "{PartitionBound strategy range default true}", nil
	}
	if len(t.keys) != len(t.membership.Lower) || len(t.keys) != len(t.membership.Upper) {
		return "", fmt.Errorf("stuntgres: invalid partition bound shape")
	}
	var out strings.Builder
	out.WriteString("{PartitionBound strategy range default false lower (")
	if err := t.appendDatums(ctx, &out, t.membership.Lower); err != nil {
		return "", err
	}
	out.WriteString(") upper (")
	if err := t.appendDatums(ctx, &out, t.membership.Upper); err != nil {
		return "", err
	}
	out.WriteString(")}")
	return out.String(), nil
}

func (t partitionBoundTree) appendDatums(ctx context.Context, out *strings.Builder, datums []partitionDatum) error {
	for i, datum := range datums {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i != 0 {
			out.WriteByte(' ')
		}
		switch datum.Kind {
		case -1:
			out.WriteString("MINVALUE")
		case 1:
			out.WriteString("MAXVALUE")
		case 0:
			usage := t.keys[i].Usage
			text, err := datum.Value.Type().Output(ctx, datum.Value)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "{type %d typmod %d collation %d value ", usage.Type.ID(), usage.Typmod, usage.Collation.ID)
			out.WriteString(strconv.Quote(text))
			out.WriteByte('}')
		default:
			return fmt.Errorf("stuntgres: invalid partition bound datum")
		}
	}
	return nil
}
