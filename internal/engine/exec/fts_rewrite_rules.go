package exec

import (
	"context"
	"errors"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const rewriteFetchSize = 100

// fetchRewriteBatch requests at most one released SPI fetch, without probing
// row 101 to discover EOF. Valid rule rows retain their immutable typed cells;
// invalid result shapes reuse one exact-width scratch row for their initial
// fetch, so a competing row error still precedes the shape diagnostic.
func fetchRewriteBatch(ctx context.Context, rows RowCursor, buffer []sqltype.Value, width int, retain bool) (int, error) {
	for i := range rewriteFetchSize {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		offset := 0
		if retain {
			offset = i * width
		}
		present, err := rows.Next(ctx, buffer[offset:offset+width])
		if err != nil {
			return i, err
		}
		if !present {
			return i, nil
		}
	}
	return rewriteFetchSize, nil
}

// ConsumeRewriteRules owns and closes the cursor before the caller restores
// lexical scratch. Fetch errors retain their original diagnostics. A deletion
// ends rule application, but not the unconditional fetch at the loop bottom.
func ConsumeRewriteRules(ctx context.Context, rows RowCursor, accumulator *sqltype.FTSRewriteRules) (value sqltype.Value, err error) {
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			value = sqltype.Value{}
		}
	}()
	schema := rows.Schema()
	if len(schema) != 2 || schema[0].Usage.Type.ID() != sqltype.TSQuery || schema[1].Usage.Type.ID() != sqltype.TSQuery {
		if _, err := fetchRewriteBatch(ctx, rows, make([]sqltype.Value, len(schema)), len(schema), false); err != nil {
			return value, err
		}
		return value, sqlError("22023", "ts_rewrite query must return two tsquery columns")
	}
	var batch [rewriteFetchSize * 2]sqltype.Value
	count, err := fetchRewriteBatch(ctx, rows, batch[:], 2, true)
	if err != nil {
		return value, err
	}
	for count > 0 {
		empty, err := accumulator.Empty()
		if err != nil {
			return value, err
		}
		if empty {
			break
		}
		for i := range count {
			if err := ctx.Err(); err != nil {
				return value, err
			}
			empty, err := accumulator.Empty()
			if err != nil {
				return value, err
			}
			if empty {
				break
			}
			if err := accumulator.Apply(batch[2*i], batch[2*i+1]); err != nil {
				return value, err
			}
		}
		count, err = fetchRewriteBatch(ctx, rows, batch[:], 2, true)
		if err != nil {
			return value, err
		}
	}
	return accumulator.Finish()
}

// RewriteSQLDiagnostic mirrors the SPI prepare/open boundary, not FETCH. Copy
// before enrichment: errors from stored expressions may be shared immutable data.
func RewriteSQLDiagnostic(err error, text string) error {
	if err == nil {
		return nil
	}
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	d := *original
	if d.InternalQuery != "" {
		return err
	}
	if d.Position != "" {
		d.InternalPosition, d.InternalQuery, d.Position = d.Position, text, ""
	} else if d.Context == "" {
		d.Context = "SQL statement \"" + text + "\""
	}
	return &d
}
