package dual

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"
)

type interfaceSet struct{ reset, valid, check bool }

func connInterfaces(c driver.Conn) interfaceSet {
	_, r := c.(driver.SessionResetter)
	_, v := c.(driver.Validator)
	_, k := c.(driver.NamedValueChecker)
	return interfaceSet{r, v, k}
}

func TestDualWrapperInterfaces(t *testing.T) {
	ctx := context.Background()
	natives := []struct {
		conn driver.Conn
		want interfaceSet
	}{
		{&fakeConn{}, interfaceSet{}},
		{struct {
			*fakeConn
			resetMixin
		}{&fakeConn{}, resetMixin{}}, interfaceSet{reset: true}},
		{struct {
			*fakeConn
			validMixin
		}{&fakeConn{}, validMixin{}}, interfaceSet{valid: true}},
		{struct {
			*fakeConn
			checkMixin
		}{&fakeConn{}, checkMixin{}}, interfaceSet{check: true}},
		{struct {
			*fakeConn
			resetMixin
			validMixin
		}{&fakeConn{}, resetMixin{}, validMixin{}}, interfaceSet{reset: true, valid: true}},
		{struct {
			*fakeConn
			resetMixin
			checkMixin
		}{&fakeConn{}, resetMixin{}, checkMixin{}}, interfaceSet{reset: true, check: true}},
		{struct {
			*fakeConn
			validMixin
			checkMixin
		}{&fakeConn{}, validMixin{}, checkMixin{}}, interfaceSet{valid: true, check: true}},
		{struct {
			*fakeConn
			resetMixin
			validMixin
			checkMixin
		}{&fakeConn{}, resetMixin{}, validMixin{}, checkMixin{}}, interfaceSet{true, true, true}},
	}
	for i, n := range natives {
		if got := connInterfaces(n.conn); got != n.want {
			t.Fatalf("native %d fixture: %+v, want %+v", i, got, n.want)
		}
		w, err := NewConnector(&fakeConnector{conn: n.conn}, &fakeConnector{conn: &fakeConn{}}, Options{}).Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := connInterfaces(w); got != n.want {
			t.Errorf("wrapper %d: %+v, want %+v", i, got, n.want)
		}
		_, prep := w.(driver.ConnPrepareContext)
		_, exec := w.(driver.ExecerContext)
		_, query := w.(driver.QueryerContext)
		_, begin := w.(driver.ConnBeginTx)
		_, ping := w.(driver.Pinger)
		_, legacyExec := w.(driver.Execer)
		_, legacyQuery := w.(driver.Queryer)
		if !prep || !exec || !query || !begin || !ping || legacyExec || legacyQuery {
			t.Errorf("wrapper %d context interfaces: prepare %t exec %t query %t begin %t ping %t execer %t queryer %t", i, prep, exec, query, begin, ping, legacyExec, legacyQuery)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}

	stmts := []struct {
		stmt        driver.Stmt
		check, conv bool
	}{
		{&fakeStmt{}, false, false},
		{stmtWithCheck{&fakeStmt{}, stmtCheckMix{}}, true, false},
		{stmtWithConverter{&fakeStmt{}, converterMix{}}, false, true},
		{stmtWithBoth{&fakeStmt{}, stmtCheckMix{}, converterMix{}}, true, true},
	}
	for i, s := range stmts {
		native := &fakeConn{stmt: func() driver.Stmt { return s.stmt }}
		w, err := NewConnector(&fakeConnector{conn: native}, &fakeConnector{conn: &fakeConn{}}, Options{}).Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st, err := w.(driver.ConnPrepareContext).PrepareContext(ctx, "q")
		if err != nil {
			t.Fatal(err)
		}
		_, check := st.(driver.NamedValueChecker)
		_, conv := st.(driver.ColumnConverter)
		_, execCtx := st.(driver.StmtExecContext)
		_, queryCtx := st.(driver.StmtQueryContext)
		if check != s.check || conv != s.conv || !execCtx || !queryCtx {
			t.Errorf("stmt %d: checker %t converter %t exec %t query %t, want checker %t converter %t", i, check, conv, execCtx, queryCtx, s.check, s.conv)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// rowsObservation is what database/sql shows for a query over a driver.
type rowsObservation struct {
	ScanType    reflect.Type
	TypeName    string
	HasLength   bool
	HasNullable bool
	HasDecimal  bool
	Values      []int64
	NextSet     bool
	Err         error
}

func observeRows(t *testing.T, db *sql.DB) rowsObservation {
	t.Helper()
	rows, err := db.Query("q")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatalf("column types %v: %v", types, err)
	}
	var o rowsObservation
	o.ScanType, o.TypeName = types[0].ScanType(), types[0].DatabaseTypeName()
	_, o.HasLength = types[0].Length()
	_, o.HasNullable = types[0].Nullable()
	_, _, o.HasDecimal = types[0].DecimalSize()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		o.Values = append(o.Values, v)
	}
	o.NextSet, o.Err = rows.NextResultSet(), rows.Err()
	return o
}

func TestDualWrapperRowsFallbacks(t *testing.T) {
	newNative := func() driver.Conn {
		return &execConn{fakeConn: &fakeConn{}, query: func(context.Context, string) (driver.Rows, error) { return oneRow(), nil }}
	}
	bare := sql.OpenDB(&fakeConnector{conn: newNative()})
	defer bare.Close()
	wrapped := sql.OpenDB(NewConnector(&fakeConnector{conn: newNative()}, &fakeConnector{conn: newNative()}, Options{}))
	defer wrapped.Close()
	want := rowsObservation{ScanType: reflect.TypeFor[any](), Values: []int64{1}}
	if got := observeRows(t, bare); !reflect.DeepEqual(got, want) {
		t.Fatalf("bare fake: %+v, want %+v", got, want)
	}
	if got := observeRows(t, wrapped); !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapper: %+v, want %+v", got, want)
	}
}
