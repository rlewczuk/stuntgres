package engine

import (
	"context"
	"database/sql/driver"
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
	fresh := New()
	_, err := e.ReplaceCatalog(ctx, fresh.CatalogRead())
	return err
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
	type dumpRelation struct {
		schema, name string
		columns      []parser.Column
		rows         [][]driver.Value
	}
	tables := make([]dumpRelation, 0)
	for a, d := range e.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok {
			if d.Kind() == NamespaceObject {
				continue
			}
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: unsupported catalog descriptor kind")
		}
		ns, ok := e.state.descriptors[r.namespace].(*namespaceDescriptor)
		if !ok {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation namespace is missing")
		}
		storage := e.storage[a]
		if storage == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation storage is missing")
		}
		tables = append(tables, dumpRelation{ns.name, r.name, append([]parser.Column(nil), r.columns...), storage.rows})
	}
	e.mu.Unlock()
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].schema != tables[j].schema {
			return tables[i].schema < tables[j].schema
		}
		return tables[i].name < tables[j].name
	})
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
		qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
		var line strings.Builder
		line.WriteString("CREATE TABLE " + qualified + " (")
		for i, c := range t.columns {
			if i > 0 {
				line.WriteString(", ")
			}
			line.WriteString(quoteIdentifier(c.Name) + " " + c.Type.SQL())
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
			line.WriteString("INSERT INTO " + qualified + " VALUES (")
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
						return sqlError("42P02", "parameters are not allowed in restore", objectNameRelation(s.Name), "")
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
	_, err = e.ReplaceCatalog(ctx, stage.CatalogRead())
	return err
}
