package core

import (
	"context"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
)

// ConvertJSONTextSearch resolves an omitted default after flag validation and
// before traversal. Parser/dictionary acquisition remains lazy for both forms.
func (h *statementHost) ConvertJSONTextSearch(ctx context.Context, explicit bool, config, document, filter sqltype.Value, filtered bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	flags := sqltype.JSONTextSearchString
	if filtered {
		var err error
		flags, err = sqltype.ParseJSONTextSearchFlags(ctx, filter)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	var input textSearchInput
	if !explicit {
		var err error
		input, err = h.resolveTextSearchInput(ctx, false, config)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	var captured textSearchCapture
	var builder fts.VectorBuilder
	position := 0
	err := sqltype.WalkJSONTextSearch(ctx, document, flags, func(text string) error {
		if captured.acquisition == nil {
			var err error
			if explicit {
				captured, err = h.captureTextSearchInput(ctx, true, config, false)
			} else {
				captured, err = captureTextSearch(ctx, input.read, input.oid)
			}
			if err != nil {
				return err
			}
		}
		emitted := false
		err := captured.walk(ctx, h.scope(), text, &position, func(item fts.LexicalPosition) {
			for _, word := range item.Lexemes {
				builder.Add(word.Text, item.Position)
				emitted = true
			}
		})
		if err != nil {
			return err
		}
		if emitted {
			position++
		}
		return nil
	})
	if err != nil {
		return sqltype.Value{}, err
	}
	vector, err := builder.Finish(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := sqltype.TSVectorValue(result, vector)
	if err != nil {
		return sqltype.Value{}, err
	}
	if captured.acquisition != nil {
		if err := captured.acquisition.accept(ctx, h); err != nil {
			return sqltype.Value{}, err
		}
	} else if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
