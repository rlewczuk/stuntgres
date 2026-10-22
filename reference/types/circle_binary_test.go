package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func circleBinaryFrame(values ...[]byte) []byte {
	var out bytes.Buffer
	out.WriteString("PGCOPY\n\xff\r\n\x00")
	out.Write(make([]byte, 8))
	for _, v := range values {
		binary.Write(&out, binary.BigEndian, int16(1))
		if v == nil {
			binary.Write(&out, binary.BigEndian, int32(-1))
		} else {
			binary.Write(&out, binary.BigEndian, int32(len(v)))
			out.Write(v)
		}
	}
	binary.Write(&out, binary.BigEndian, int16(-1))
	return out.Bytes()
}

func TestReferenceCircleBinary(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_binary(v circle)", 0))
		pool := p.Native
		if b.PG {
			pool = p.CopyPostgres
		}
		stream, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		decode := func(s string) []byte {
			v, err := hex.DecodeString(s)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		finite := decode("3ff000000000000040000000000000004008000000000000")
		special := decode("80000000000000007ff00000000000000000000000000000")
		frame := circleBinaryFrame(finite, nil, special)
		result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY circle_binary FROM STDIN WITH(FORMAT binary)", bytes.NewReader(frame))
		if err != nil || result.Count != 3 || result.CommandTag != "COPY 3" {
			t.Fatalf("binary import %+v %v", result, err)
		}
		s := circleSelect(b, "SELECT v FROM circle_binary", []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(1,2),3>"}, {nil}, {"<(-0,Infinity),0>"}})
		s.Bag = true
		run(s)
		// Export a single known row: no physical row-order assumption.
		var out bytes.Buffer
		q := "COPY (SELECT v FROM circle_binary WHERE radius(v)=3) TO STDOUT WITH(FORMAT binary)"
		if b.PG {
			err = stream.Raw(func(raw any) error {
				conn, ok := raw.(*stdlib.Conn)
				if !ok {
					return fmt.Errorf("PG COPY raw %T", raw)
				}
				tag, e := conn.Conn().PgConn().CopyTo(p.Ctx, &out, q)
				if e == nil {
					result = stuntgres.CopyResult{Count: tag.RowsAffected(), CommandTag: tag.String()}
				}
				return e
			})
		} else {
			result, err = stuntgres.CopyTo(p.Ctx, stream, q, &out)
		}
		if err != nil || result.Count != 1 || result.CommandTag != "COPY 1" {
			t.Fatalf("binary export %+v %v", result, err)
		}
		if want := circleBinaryFrame(finite); !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("binary export %x want %x", out.Bytes(), want)
		}
		negative := decode("00000000000000000000000000000000bff0000000000000")
		result, err = fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY circle_binary FROM STDIN WITH(FORMAT binary)", bytes.NewReader(circleBinaryFrame(finite, negative)))
		want := circleDiagnostic("22P03", `invalid radius in external "circle" value`)
		want.Context = "COPY circle_binary, line 2, column v"
		fixture.AssertDiagnostic(t, err, &want)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatalf("failed COPY published %+v", result)
		}
		run(fixture.Select("SELECT count(*) AS n FROM circle_binary", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}
