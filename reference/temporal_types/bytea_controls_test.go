package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type referenceByteaFailedReader struct{ err error }

func (r referenceByteaFailedReader) Read([]byte) (int, error) { return 0, r.err }

const referenceByteaControlSeed = `INSERT INTO t VALUES(1,'',''),(2,'\x0080ff5c27','\x0080ff'),(3,NULL,'\xff')`

func referenceByteaControlRows() [][]any {
	return [][]any{{[]byte{}, []byte{}}, {[]byte{0, 128, 255, 92, 39}, []byte{0, 128, 255}}, {nil, []byte{255}}}
}
func referenceByteaControlValues() [][]any {
	return [][]any{{[]byte{}}, {[]byte{0, 128, 255, 92, 39}}, {nil}}
}

// Failed staged replacements must leave the independently seeded catalog intact.
func referenceByteaFailedRestores(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	sentinel := errors.New("reader failed")
	for _, failure := range []struct {
		reader io.Reader
		want   error
	}{
		{strings.NewReader(`CREATE TABLE replacement(v BYTEA);INSERT INTO replacement VALUES('\x41');INSERT INTO replacement VALUES('\q');`), nil},
		{referenceByteaFailedReader{sentinel}, sentinel},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, failure.reader)
		if err == nil || failure.want != nil && !errors.Is(err, failure.want) {
			t.Fatal(err)
		}
		if failure.want != nil {
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("reader fabricated SQLSTATE", err)
			}
		}
		referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
	}
}

func TestReferenceByteaControlsRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER NOT NULL,v BYTEA,r BYTEA NOT NULL)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(referenceByteaControlSeed, 3))
			if !b.PG {
				func() {
					// Keep settings and restored TEXT on one middleware-recorded session.
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
					}()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					for _, piece := range []string{`"v" BYTEA, "r" BYTEA CONSTRAINT "t_r_not_null" NOT NULL`, `'\x'`, `'\x0080ff5c27'`, `'\x0080ff'`, `'\xff'`, "NULL"} {
						if !strings.Contains(dump.String(), piece) {
							t.Fatal(piece, dump.String())
						}
					}
					if _, err := conn.ExecContext(p.Ctx, "SET bytea_output=escape"); err != nil {
						t.Fatal(err)
					}
					var escape bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &escape); err != nil || !bytes.Equal(dump.Bytes(), escape.Bytes()) {
						t.Fatal(escape.String(), err)
					}
					if _, err := conn.ExecContext(p.Ctx, "SET standard_conforming_strings=off"); err != nil {
						t.Fatal(err)
					}
					referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("INSERT INTO t VALUES(4,$1,$1)", 1, []byte{77}))
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v::text AS rendered FROM t ORDER BY id", []string{"rendered"}, []string{"TEXT"}, [][]any{{""}, {`\000\200\377\\'`}, {nil}}))
					for _, setting := range []struct{ name, want string }{{"bytea_output", "escape"}, {"standard_conforming_strings", "off"}} {
						var got string
						if err := conn.QueryRowContext(p.Ctx, "SHOW "+setting.name).Scan(&got); err != nil || got != setting.want {
							t.Fatal(got, err)
						}
					}
				}()
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
			s := fixture.ImplicitError("INSERT INTO t VALUES(4,NULL,$1),(5,NULL,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "r" of relation "t" violates not-null constraint`, Detail: "Failing row contains (5, null, null).", Schema: "public", Table: "t", Column: "r"}, []byte{})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
		})
	}
}

func TestReferenceByteaControlsRetainedOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER NOT NULL,v BYTEA,r BYTEA NOT NULL)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(referenceByteaControlSeed, 3))
			if !b.PG {
				conn, err := p.Native.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
				}()
				settings, err := p.Native.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := settings.Close(); err != nil {
						t.Error(err)
					}
				}()
				err = conn.Raw(func(raw any) error {
					if _, err := raw.(driver.ExecerContext).ExecContext(p.Ctx, "SET bytea_output=escape", nil); err != nil {
						return err
					}
					rendered, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v::text AS rendered FROM t ORDER BY id", nil)
					if err != nil {
						return err
					}
					defer rendered.Close()
					if _, err := raw.(driver.ExecerContext).ExecContext(p.Ctx, "SET bytea_output=hex", nil); err != nil {
						return err
					}
					// This public raw handle remains borrowed through every catalog transition.
					held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM t ORDER BY id", nil)
					if err != nil {
						return err
					}
					defer func() {
						if err := held.Close(); err != nil {
							t.Error(err)
						}
					}()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						return err
					}
					if _, err := settings.ExecContext(p.Ctx, "SET bytea_output=escape"); err != nil {
						return err
					}
					var escape bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &escape); err != nil {
						return err
					}
					if !bytes.Equal(dump.Bytes(), escape.Bytes()) {
						t.Fatal("canonical dump changed")
					}
					if _, err := settings.ExecContext(p.Ctx, "SET standard_conforming_strings=off"); err != nil {
						return err
					}
					if _, err := settings.ExecContext(p.Ctx, "INSERT INTO t VALUES(4,$1,$1)", []byte{77}); err != nil {
						return err
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						return err
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						return err
					}
					for _, setting := range []struct{ name, want string }{{"bytea_output", "escape"}, {"standard_conforming_strings", "off"}} {
						var got string
						if err := settings.QueryRowContext(p.Ctx, "SHOW "+setting.name).Scan(&got); err != nil || got != setting.want {
							t.Fatal(got, err)
						}
					}
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
					s := fixture.ImplicitError("INSERT INTO t VALUES(4,NULL,$1),(5,NULL,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "r" of relation "t" violates not-null constraint`, Detail: "Failing row contains (5, null, null).", Schema: "public", Table: "t", Column: "r"}, []byte{})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
					textDst := make([]driver.Value, 1)
					for _, want := range []any{"", `\000\200\377\\'`, nil} {
						if err := rendered.Next(textDst); err != nil || !reflect.DeepEqual(textDst[0], want) {
							t.Fatal("retained escape TEXT", textDst, want, err)
						}
					}
					if err := rendered.Next(textDst); err != io.EOF {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(rendered.Columns(), []string{"rendered"}) || rendered.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "TEXT" || rendered.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
						t.Fatal("retained TEXT metadata")
					}
					dst := make([]driver.Value, 1)
					for _, want := range referenceByteaControlValues() {
						if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst[0], want[0]) {
							t.Fatal(dst, err)
						}
						if v, ok := dst[0].([]byte); ok && len(v) > 0 {
							v[0] = 99
						}
					}
					if err := held.Next(dst); err != io.EOF {
						t.Fatal(err)
					}
					if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BYTEA" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[[]byte]() {
						t.Fatal("retained schema")
					}
					referenceByteaFailedRestores(t, p, b)
					canceled, cancel := context.WithCancel(p.Ctx)
					cancel()
					if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if _, err := p.Native.ExecContext(canceled, "INSERT INTO t VALUES(4,$1,$1)", []byte{1}); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					cursorCtx, stop := context.WithCancel(p.Ctx)
					defer stop()
					cursor, err := raw.(driver.QueryerContext).QueryContext(cursorCtx, "SELECT v FROM t ORDER BY id", nil)
					if err != nil {
						return err
					}
					defer func() {
						if err := cursor.Close(); err != nil {
							t.Error(err)
						}
					}()
					stop()
					if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), true)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", true, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
		})
	}
}

func TestReferenceByteaControlsFailuresAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER NOT NULL,v BYTEA,r BYTEA NOT NULL)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(referenceByteaControlSeed, 3))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				referenceByteaFailedRestores(t, p, b)
				if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, err := p.Native.ExecContext(canceled, "INSERT INTO t VALUES(4,$1,$1)", []byte{1}); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				conn, err := p.Native.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
				}()
				err = conn.Raw(func(raw any) error {
					ctx, stop := context.WithCancel(p.Ctx)
					defer stop()
					cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY id", nil)
					if err != nil {
						return err
					}
					defer func() {
						if err := cursor.Close(); err != nil {
							t.Error(err)
						}
					}()
					stop()
					if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.DB.ExecContext(canceled, "INSERT INTO t VALUES(4,$1,$1)", []byte{1}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r FROM t ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, referenceByteaControlRows(), false)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, referenceByteaControlValues(), false)
		})
	}
}
