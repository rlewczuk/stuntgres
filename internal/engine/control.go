package engine

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	e.tables = make(map[string]*table)
	return nil
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (e *Engine) Dump(ctx context.Context, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	tables := make([]table, 0, len(e.tables))
	for _, t := range e.tables {
		tables = append(tables, *t)
	}
	e.mu.Unlock()
	sort.Slice(tables, func(i, j int) bool { return tables[i].name < tables[j].name })
	emit := func(s string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.WriteString(dst, s)
		if err != nil {
			return err
		}
		if n != len(s) {
			return io.ErrShortWrite
		}
		return ctx.Err()
	}
	for _, t := range tables {
		var line strings.Builder
		line.WriteString("CREATE TABLE ")
		line.WriteString(quoteIdentifier(t.name))
		line.WriteString(" (")
		for i, c := range t.columns {
			if i > 0 {
				line.WriteString(", ")
			}
			line.WriteString(quoteIdentifier(c.Name))
			line.WriteByte(' ')
			line.WriteString(c.Type.SQL())
			if !c.Nullable {
				line.WriteString(" NOT NULL")
			}
		}
		line.WriteString(");\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		for _, row := range t.rows {
			line.Reset()
			line.WriteString("INSERT INTO ")
			line.WriteString(quoteIdentifier(t.name))
			line.WriteString(" VALUES (")
			for i, v := range row {
				if i > 0 {
					line.WriteString(", ")
				}
				switch v := v.(type) {
				case nil:
					line.WriteString("NULL")
				case int64:
					line.WriteString(strconv.FormatInt(v, 10))
				case bool:
					if v {
						line.WriteString("TRUE")
					} else {
						line.WriteString("FALSE")
					}
				case string:
					line.WriteByte('\'')
					line.WriteString(strings.ReplaceAll(v, "'", "''"))
					line.WriteByte('\'')
				}
			}
			line.WriteString(");\n")
			if err := emit(line.String()); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
func (e *Engine) Restore(ctx context.Context, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	statements, err := parser.Parse(string(data))
	if err != nil {
		return err
	}
	stage := New()
	for _, stmt := range statements {
		switch s := stmt.(type) {
		case *parser.CreateTable:
		case *parser.Insert:
			for _, row := range s.Rows {
				for _, v := range row {
					if v.Kind == parser.ParameterValue {
						return sqlError("42P02", "parameters are not allowed in restore", s.Name, "")
					}
				}
			}
		default:
			return sqlError("0A000", "restore accepts only CREATE and INSERT", "", "")
		}
		if _, err = stage.Execute(ctx, stmt, nil); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	e.tables = stage.tables
	return nil
}
