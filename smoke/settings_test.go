package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
)

func settingsScalar(ctx context.Context, c *sql.Conn, q, column string, want any, prepared bool, args ...any) error {
	var r *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			return e
		}
		defer s.Close()
		r, err = s.QueryContext(ctx, args...)
	} else {
		r, err = c.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return err
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		return err
	}
	types, err := r.ColumnTypes()
	if err != nil {
		return err
	}
	if len(types) != 1 {
		return fmt.Errorf("%s result types %v", q, types)
	}
	nullable, known := types[0].Nullable()
	if !reflect.DeepEqual(cols, []string{column}) || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeOf("") || !known || nullable != strings.HasPrefix(q, "SELECT") {
		return fmt.Errorf("metadata %s: %v %v", q, cols, types)
	}
	if !r.Next() {
		return fmt.Errorf("missing row %s: %v", q, r.Err())
	}
	var v any
	if err = r.Scan(&v); err != nil {
		return err
	}
	if !reflect.DeepEqual(v, want) {
		return fmt.Errorf("%s got %#v want %#v", q, v, want)
	}
	if r.Next() || r.Err() != nil {
		return fmt.Errorf("%s EOF: %v", q, r.Err())
	}
	return r.Close()
}
func settingsDiagnostic(err error, code, message, hint string) error {
	return settingsFullDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint})
}
func settingsFullDiagnostic(err error, want stuntgres.Error) error {
	var d *stuntgres.Error
	if !errors.As(err, &d) || *d != want {
		return fmt.Errorf("diagnostic got %#v (%v) want %#v", d, err, want)
	}
	return nil
}
