package testdecoding

import (
	"context"
	"strings"

	"stuntgres/internal/engine/logical"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func identifier(ctx context.Context, s string) (string, error) {
	quoted, err := parser.IdentifierNeedsQuotesContext(ctx, s)
	if err != nil {
		return "", err
	}
	if quoted {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`, nil
	}
	return s, nil
}
func relation(ctx context.Context, r *logical.Relation) (string, error) {
	schema, err := identifier(ctx, r.Schema)
	if err != nil {
		return "", err
	}
	name, err := identifier(ctx, r.Name)
	return schema + "." + name, err
}
func tuple(ctx context.Context, env sqltype.OutputEnvironment, b *strings.Builder, r *logical.Relation, values []sqltype.Value, old bool) error {
	if values == nil {
		b.WriteString(" (no-tuple-data)")
		return nil
	}
	for i, c := range r.Columns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.Number < 0 || i >= len(values) || c.Usage.Type == nil {
			continue
		}
		v := values[i]
		if old && v.IsNull() {
			continue
		}
		name, err := identifier(ctx, c.Name)
		if err != nil {
			return err
		}
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteByte('[')
		b.WriteString(c.TypeName)
		b.WriteString("]:")
		if v.IsNull() {
			b.WriteString("null")
			continue
		}
		text, err := c.Usage.Type.OutputWithEnvironment(ctx, v, env)
		if err != nil {
			return err
		}
		switch c.Usage.Type.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Oid, sqltype.Float4, sqltype.Float8, sqltype.Numeric:
			b.WriteString(text)
		case sqltype.Bool:
			if text == "t" {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case sqltype.Bit, sqltype.VarBit:
			b.WriteString("B'")
			b.WriteString(text)
			b.WriteByte('\'')
		default:
			b.WriteByte('\'')
			for j := range len(text) {
				if j&255 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if text[j] == '\'' {
					b.WriteByte('\'')
				}
				b.WriteByte(text[j])
			}
			b.WriteByte('\'')
		}
	}
	return nil
}
func formatChange(ctx context.Context, env sqltype.OutputEnvironment, c *logical.Change) (string, error) {
	var b strings.Builder
	b.WriteString("table ")
	if c.Kind == logical.Truncate {
		for i, r := range c.Truncated {
			if i > 0 {
				b.WriteString(", ")
			}
			name, err := relation(ctx, r)
			if err != nil {
				return "", err
			}
			b.WriteString(name)
		}
		b.WriteString(": TRUNCATE:")
		if c.RestartIdentity {
			b.WriteString(" restart_seqs")
		}
		if c.Cascade {
			b.WriteString(" cascade")
		}
		if !c.RestartIdentity && !c.Cascade {
			b.WriteString(" (no-flags)")
		}
		return b.String(), nil
	}
	name, err := relation(ctx, c.Relation)
	if err != nil {
		return "", err
	}
	b.WriteString(name)
	switch c.Kind {
	case logical.Insert:
		b.WriteString(": INSERT:")
		err = tuple(ctx, env, &b, c.Relation, c.New, false)
	case logical.Update:
		b.WriteString(": UPDATE:")
		if c.OldTuple != logical.OldNone {
			b.WriteString(" old-key:")
			if err = tuple(ctx, env, &b, c.Relation, c.Old, true); err != nil {
				return "", err
			}
			b.WriteString(" new-tuple:")
		}
		err = tuple(ctx, env, &b, c.Relation, c.New, false)
	case logical.Delete:
		b.WriteString(": DELETE:")
		err = tuple(ctx, env, &b, c.Relation, c.Old, true)
	}
	return b.String(), err
}
