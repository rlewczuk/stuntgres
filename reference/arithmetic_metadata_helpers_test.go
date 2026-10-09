package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

type referenceArithmeticMetadataExecutor struct {
	referenceExecutor
	t     *testing.T
	pg    bool
	types []string
}

func (e referenceArithmeticMetadataExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := e.referenceExecutor.QueryContext(ctx, query, args...)
	if err != nil {
		return rows, err
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		e.t.Fatal(err)
	}
	if len(meta) != len(e.types) {
		rows.Close()
		e.t.Fatalf("metadata width %d want %d", len(meta), len(e.types))
	}
	for i, typ := range e.types {
		want := reflect.TypeOf(int64(0))
		switch typ {
		case "NUMERIC":
			want = reflect.TypeFor[string]()
		case "FLOAT4", "FLOAT8":
			want = reflect.TypeFor[float64]()
		}
		if e.pg {
			switch typ {
			case "INT2":
				want = reflect.TypeOf(int16(0))
			case "INT4":
				want = reflect.TypeOf(int32(0))
			case "NUMERIC":
				want = reflect.TypeFor[any]()
			case "FLOAT4":
				want = reflect.TypeFor[float32]()
			}
		}
		if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != want {
			rows.Close()
			e.t.Fatalf("column %d type=%s scan=%v want %s/%v", i, meta[i].DatabaseTypeName(), meta[i].ScanType(), typ, want)
		}
		if _, known := meta[i].Nullable(); known {
			rows.Close()
			e.t.Fatal("computed nullability unexpectedly known")
		}
		if _, known := meta[i].Length(); known {
			rows.Close()
			e.t.Fatal("computed length unexpectedly known")
		}
		if typ == "NUMERIC" {
			precision, scale, known := meta[i].DecimalSize()
			if e.pg {
				if !known || precision != 65535 || scale != 65531 {
					rows.Close()
					e.t.Fatalf("derived NUMERIC precision=%d scale=%d known=%v", precision, scale, known)
				}
			} else if known {
				rows.Close()
				e.t.Fatal("derived native NUMERIC typmod leaked")
			}
		} else if _, _, known := meta[i].DecimalSize(); known {
			rows.Close()
			e.t.Fatal("non-NUMERIC decimal size unexpectedly known")
		}
	}
	return rows, nil
}

func referenceArithmeticMetadata(t *testing.T, p *referencePair, step referenceStep) {
	t.Helper()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				func() {
					var executor referenceExecutor = backend.db
					if prepared {
						stmt, err := backend.db.PrepareContext(p.ctx, step.query)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := stmt.Close(); err != nil {
								t.Error(err)
							}
						}()
						executor = referenceHeldStatement{backend.db, stmt, step.query}
					}
					referenceRunStep(t, p.ctx, referenceArithmeticMetadataExecutor{executor, t, backend.pg, step.types}, backend.recorder, step)
				}()
			}
		})
	}
}
