package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSettingsCallOutputLabelsNativeLifecycle(t *testing.T) {
	settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE label_lifetime(v INT)", 0))
		run(fixture.Exec("INSERT INTO label_lifetime VALUES(7)", 1))
		run(fixture.Exec("SET application_name='label-live'", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
		reader := `SELECT pg_catalog.current_setting($1) AS "ReadValue"`
		writer := `SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`
		read := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, "held", reader)
		write := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, "held", writer)
		check := func(value string) {
			read(settingsLabelValue(b.PG, reader, "ReadValue", value, "application_name"))
			settingsLabelState(t, p, b, owner, peer, value, "label-peer")
		}
		check("label-live")
		write(settingsLabelValue(b.PG, writer, "SetValue", "label-new", "application_name", "label-new", false))
		check("label-new")
		if !b.PG {
			// Public driver rows detach from their physical owner. database/sql
			// itself holds a Conn release callback while Rows is live, so retain
			// this witness at the public driver interface, not a blocked Conn.Close.
			oldOwner, err := p.NativeConnector.Connect(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if oldOwner != nil {
					oldOwner.Close()
				}
			}()
			if _, err := oldOwner.(driver.ExecerContext).ExecContext(p.Ctx, "SET application_name='label-old'", nil); err != nil {
				t.Fatal(err)
			}
			old, err := oldOwner.(driver.QueryerContext).QueryContext(p.Ctx, `SELECT current_setting('application_name') AS "OldValue"`, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata := func() {
				if !reflect.DeepEqual(old.Columns(), []string{"OldValue"}) {
					t.Fatal(old.Columns())
				}
				if old.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "TEXT" || old.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
					t.Fatal("old TEXT descriptor changed")
				}
				if n, known := old.(driver.RowsColumnTypeNullable).ColumnTypeNullable(0); !n || !known {
					t.Fatal(n, known)
				}
				if n, known := old.(driver.RowsColumnTypeLength).ColumnTypeLength(0); n != math.MaxInt64 || !known {
					t.Fatal(n, known)
				}
				if p, s, known := old.(driver.RowsColumnTypePrecisionScale).ColumnTypePrecisionScale(0); p != 0 || s != 0 || known {
					t.Fatal(p, s, known)
				}
			}
			metadata()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			check("label-new")
			metadata()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			check("label-new")
			metadata()
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("label-new")
			metadata()
			if err := oldOwner.Close(); err != nil {
				t.Fatal(err)
			}
			oldOwner = nil
			metadata()
			values := make([]driver.Value, 1)
			if err := old.Next(values); err != nil || values[0] != "label-old" {
				t.Fatal(values, err)
			}
			if err := old.Next(values); err != io.EOF {
				t.Fatal("old result EOF", err)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/restore-destination")
			if err != nil {
				t.Fatal(err)
			}
			controls := sql.OpenDB(connector)
			defer controls.Close()
			destination, recorder := fixture.RecordedNativePool(t, connector)
			defer destination.Close()
			var capturedDefault string
			if err := destination.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&capturedDefault); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, controls, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, destination, recorder, settingsLabelValue(false, `SELECT current_setting('application_name') AS value`, "value", capturedDefault))
			fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT v FROM label_lifetime", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		run(fixture.Select("SELECT v FROM label_lifetime", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		write(settingsLabelValue(b.PG, writer, "SetValue", "label-final", "application_name", "label-final", false))
		check("label-final")
	})
	// PostgreSQL has no native control API, and pq pre-cancel nil-output
	// behavior is not a portable guarantee. Keep cancellation native-only in
	// this shared definition, after the independent shared SQL phase above.
	t.Run("native-cancellation", func(t *testing.T) {
		p := fixture.NewScenario(t)
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, false, &notices)
		peer, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		run(fixture.Exec("SET application_name='label-safe'", 0))
		if _, err := peer.ExecContext(p.Ctx, "SET application_name='label-peer'"); err != nil {
			t.Fatal(err)
		}
		query := `SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`
		stmt, err := owner.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		expired, expire := context.WithDeadline(p.Ctx, time.Unix(0, 0))
		defer expire()
		for _, tc := range []struct {
			ctx  context.Context
			want error
		}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
			before := len(notices)
			for _, call := range []func() error{
				func() error {
					rows, err := owner.QueryContext(tc.ctx, query, "application_name", strings.Repeat("a", 64), false)
					if rows != nil {
						rows.Close()
						t.Fatal("canceled direct query exposed rows")
					}
					return err
				},
				func() error {
					prepared, err := owner.PrepareContext(tc.ctx, query)
					if prepared != nil {
						prepared.Close()
						t.Fatal("canceled Prepare exposed statement")
					}
					return err
				},
				func() error {
					rows, err := stmt.QueryContext(tc.ctx, "application_name", strings.Repeat("a", 64), false)
					if rows != nil {
						rows.Close()
						t.Fatal("canceled held execution exposed rows")
					}
					return err
				},
			} {
				err := call()
				if !errors.Is(err, tc.want) {
					t.Fatal(err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation has SQLSTATE", err)
				}
				if len(notices) != before {
					t.Fatal("cancellation emitted notices", notices[before:])
				}
				run(settingsLabelValue(false, `SELECT current_setting('application_name') AS value`, "value", "label-safe"))
				var value string
				if err := peer.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&value); err != nil || value != "label-peer" {
					t.Fatal(value, err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: query}, recorder, settingsLabelValue(false, query, "SetValue", "label-healthy", "application_name", "label-healthy", false))
		run(settingsLabelValue(false, `SELECT current_setting('application_name') AS value`, "value", "label-healthy"))
	})
}
