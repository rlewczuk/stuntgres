package tsearch

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// textSearchInput owns an immutable catalog generation and the configuration
// identity resolved within it, but no parser/dictionary acquisition.
type textSearchInput struct {
	read catalog.Read
	oid  uint32
}

// Engine.Mu precedes effective settings capture. The returned input retains no
// mutable session state; runtime acquisition and callbacks use its generation.
func resolveTextSearchInput[A Authority[A]](h Host[A], ctx context.Context, explicit bool, value sqltype.Value) (textSearchInput, error) {
	if err := ctx.Err(); err != nil {
		return textSearchInput{}, err
	}
	var oid uint32
	var err error
	if explicit {
		oid, err = value.RegTSOID()
		if err != nil {
			return textSearchInput{}, err
		}
	}
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	read := h.PrivateCatalogLocked(h.PendingCatalog())
	if !explicit {
		_, input, failure := h.TextSearchInputLocked()
		err = failure
		if err == nil {
			name, _ := h.Operation().Setting("default_text_search_config")
			oid, err = input.ResolveDefaultConfig(ctx, name)
		}
	}
	if err != nil {
		return textSearchInput{}, err
	}
	return textSearchInput{read: read, oid: oid}, nil
}

func captureTextSearchInput[A Authority[A]](h Host[A], ctx context.Context, explicit bool, value sqltype.Value, missingEmpty bool) (Capture[A], error) {
	input, err := resolveTextSearchInput(h, ctx, explicit, value)
	if err != nil {
		return Capture[A]{}, err
	}
	if missingEmpty {
		if _, ok := input.read.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(input.oid)}); !ok {
			return Capture[A]{}, nil
		}
	}
	return CaptureConfiguration[A](ctx, input.read, input.oid)
}

// Debug is ts_debug's rows for text.
func Debug[A Authority[A]](h Host[A], ctx context.Context, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) (rows []sqltype.Value, err error) {
	// Provider failures arise inside the actual SQL-standard debug statement,
	// after caller argument evaluation. Preserve its frame without mutating errors.
	defer func() { err = exec.SQLRoutineStatementContext(err, "ts_debug", 1) }()
	captured, err := captureTextSearchInput(h, ctx, explicit, value, true)
	if err != nil {
		return nil, err
	}
	if captured.config == nil {
		return nil, ctx.Err()
	}
	tokens, err := captured.parser.Parse(ctx, text)
	if err != nil {
		return nil, err
	}
	types, err := captured.parser.TokenTypes(ctx)
	if err != nil {
		return nil, err
	}
	// Mapping arrays are immutable and shared only within this invocation.
	arrays := make(map[int32]sqltype.Value)
	rows = make([]sqltype.Value, 0, len(tokens))
	for _, token := range tokens {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if token.ID < 1 || int(token.ID) > len(types) {
			return nil, fmt.Errorf("stuntgres: invalid text parser token identity")
		}
		mapping := captured.config.Mapping(token.ID)
		array, ok := arrays[token.ID]
		if !ok {
			values := make([]sqltype.Value, len(mapping))
			for i, address := range mapping {
				values[i], err = sqltype.RegTSValue(result.CompositeMember(3).Usage.Type.Element(), uint32(address.ObjectID))
				if err != nil {
					return nil, err
				}
			}
			var dims []sqltype.ArrayDimension
			if len(values) > 0 {
				dims = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
			}
			array, err = sqltype.OwnedArrayValue(result.CompositeMember(3).Usage.Type, dims, values)
			if err != nil {
				return nil, err
			}
			arrays[token.ID] = array
		}
		dictionary := sqltype.Null(result.CompositeMember(4).Usage.Type)
		lexemes := sqltype.Null(result.CompositeMember(5).Usage.Type)
		recognized := false
		for i, address := range mapping {
			runtime, err := captured.acquisition.dictionary(h, address)
			if err != nil {
				return nil, err
			}
			lexical, err := runtime.Lexize(ctx, token.Text)
			if err != nil {
				return nil, err
			}
			if i == 0 || !recognized && lexical.Recognized {
				dictionary, err = sqltype.RegTSValue(result.CompositeMember(4).Usage.Type, uint32(address.ObjectID))
				if err != nil {
					return nil, err
				}
				lexemes, err = sqltype.LexicalArray(result.CompositeMember(5).Usage.Type, lexical)
				if err != nil {
					return nil, err
				}
			}
			recognized = recognized || lexical.Recognized
		}
		descriptor := types[token.ID-1]
		row, err := sqltype.OwnedCompositeValue(result, []sqltype.Value{sqltype.String(result.CompositeMember(0).Usage.Type, descriptor.Alias), sqltype.String(result.CompositeMember(1).Usage.Type, descriptor.Description), sqltype.String(result.CompositeMember(2).Usage.Type, token.Text), array, dictionary, lexemes})
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := captured.acquisition.accept(ctx, h); err != nil {
		return nil, err
	}
	return rows, nil
}
