package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const referenceByteaPilotInsert = "INSERT INTO t VALUES(1,''),(2,'\\x'),(3,'\\x00aAfF'),(4,'\\x 00\tAA\nff\r'),(5,'\\000\\177\\200\\377'),(6,'\\\\'),(7,'''') ,(8,'é')"

func referenceByteaPilotLiterals() [][]any {
	return [][]any{{[]byte{}}, {[]byte{}}, {[]byte{0, 170, 255}}, {[]byte{0, 170, 255}}, {[]byte{0, 127, 128, 255}}, {[]byte{92}}, {[]byte{39}}, {[]byte{195, 169}}}
}
func referenceByteaBindCases() []struct{ arg, want any } {
	return []struct{ arg, want any }{
		{string([]byte{0, 128, 255}), []byte{0, 128, 255}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}},
		{`\x41`, []byte{92, 120, 52, 49}}, {[]byte(`\000`), []byte{92, 48, 48, 48}},
		{nil, nil}, {[]byte(nil), nil}, {[]byte{}, []byte{}}, {"", []byte{}},
	}
}

func TestReferenceByteaBindOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			referenceByteaExec(t, ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0))
			referenceByteaExec(t, ctx, b.DB, b, fixture.Exec(referenceByteaPilotInsert, 8))
			want := referenceByteaPilotLiterals()
			for _, prepared := range []bool{false, true} {
				func() {
					x := fixture.Executor(b.DB)
					q := "INSERT INTO t VALUES($1,$2)"
					if prepared {
						stmt, err := b.DB.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := stmt.Close(); err != nil {
								t.Error(err)
							}
						}()
						x = fixture.HeldStatement{b.DB, stmt, q}
					}
					for _, tc := range referenceByteaBindCases() {
						referenceByteaExec(t, ctx, x, b, fixture.Exec(q, 1, len(want)+1, tc.arg))
						b.Recorder.Mu.Lock()
						op := b.Recorder.Operations[len(b.Recorder.Operations)-1]
						arg := op.Args[1].Value
						b.Recorder.Mu.Unlock()
						if !reflect.DeepEqual(arg, tc.arg) {
							t.Fatalf("owned bind %#v want %#v", arg, tc.arg)
						}
						if bytes, ok := tc.arg.([]byte); ok && len(bytes) > 0 {
							first := bytes[0]
							bytes[0] ^= 255
							if arg.([]byte)[0] != first {
								t.Fatal("source mutation changed recorded bind")
							}
						}
						want = append(want, []any{tc.want})
					}
				}()
			}
			referenceByteaRaw(t, ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, want, false)
		})
	}
}

func TestReferenceByteaRawMetadataAndOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(referenceByteaPilotInsert, 8))
			want := referenceByteaPilotLiterals()
			for _, prepared := range []bool{false, true} {
				for _, tc := range referenceByteaBindCases() {
					s := fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, len(want)+1, tc.arg)
					s.Prepared = prepared
					referenceByteaExec(t, p.Ctx, b.DB, b, s)
					want = append(want, []any{tc.want})
				}
			}
			for _, prepared := range []bool{false, true} {
				referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", prepared, []string{"v"}, []int{1}, want, true)
				referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t WHERE v=NULL", prepared, []string{"v"}, []int{1}, nil, true)
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, want, false)
		})
	}
}
