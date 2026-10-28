package session

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func xmlProtocolType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.XML {
			return d
		}
	}
	t.Fatal("missing XML type")
	return nil
}
func TestXMLProtocolOutputOwnership(t *testing.T) {
	d := xmlProtocolType(t)
	v, err := d.InputWithEnvironment(context.Background(), `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`, -1, sqltype.InputEnvironment{XMLMode: sqltype.XMLDocument})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var collector OutputNoticeCollector
	output := sqltype.OutputEnvironment{XMLNotice: func(n sqlstate.Error) { collector.Add(n); cancel() }}
	_, _, err = EncodeProtocolRow(ctx, []sqltype.Value{v, v}, nil, output, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("later canceled conversion", err)
	}
	notices := collector.Detach()
	if len(notices) != 1 || notices[0].Code != "XX001" || notices[0].Detail != "Space required." {
		t.Fatalf("committed warning lost: %#v", notices)
	}
	if len(collector.Detach()) != 0 {
		t.Fatal("collector replayed records")
	}
}

func TestXMLProtocolOutputEncoding(t *testing.T) {
	d := xmlProtocolType(t)
	v, err := d.InputWithEnvironment(context.Background(), "<a>é</a>", -1, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		encoding string
		format   int16
		want     string
	}{
		{"LATIN1", 0, "<a>\xe9</a>"},
		{"LATIN1", 1, "<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>"},
		{"UTF8", 0, "<a>é</a>"},
		{"UTF8", 1, "<a>é</a>"},
		{"SQL_ASCII", 1, "<a>é</a>"},
	} {
		in, out := sqltype.EncodingConversionHandler{}, sqltype.EncodingConversionHandler{}
		if tc.encoding == "LATIN1" {
			in, _ = sqltype.RegisteredEncodingConversion("iso8859_1_to_utf8")
			out, _ = sqltype.RegisteredEncodingConversion("utf8_to_iso8859_1")
		}
		policy, err := sqltype.NewClientEncodingPolicy(sqltype.EncodingID(tc.encoding), in, out)
		if err != nil {
			t.Fatal(err)
		}
		fields, _, err := EncodeProtocolRow(context.Background(), []sqltype.Value{v}, []int16{tc.format}, sqltype.OutputEnvironment{ClientEncoding: tc.encoding, Transport: policy}, nil, nil)
		if err != nil || len(fields) != 1 || string(fields[0].Bytes) != tc.want {
			t.Fatalf("encoding=%s format=%d fields=%#v error=%v", tc.encoding, tc.format, fields, err)
		}
	}
}

func TestXMLProtocolInputOwnership(t *testing.T) {
	d := xmlProtocolType(t)
	decode := func(raw string, format int16, mode sqltype.XMLMode, output sqltype.OutputEnvironment) (sqltype.Value, ProtocolContext, error) {
		t.Helper()
		body := binary.BigEndian.AppendUint32(nil, uint32(len(raw)))
		body = append(body, raw...)
		reader := &codecInput{data: body}
		frame := make([]sqltype.Value, 1)
		var log ProtocolContext
		env := sqltype.InputEnvironment{XMLMode: mode, XMLInput: XMLInput{}}
		err := ImportProtocolParameters(context.Background(), output, reader, []int16{format}, 1, []sqltype.Usage{d.Usage()}, frame, env, &log, ProtocolBindInput)
		// The accepted payload and decoded log must outlive the borrowed body.
		clear(body)
		return frame[0], log, err
	}
	value, log, err := decode(`<?xml version="1.0" encoding="LATIN1"?><a>`+"\xe9</a>", 1, sqltype.XMLContent, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := d.Output(context.Background(), value)
	if err != nil || text != "<a>é</a>" || !log.Parameters[0].Decoded || string(log.Parameters[0].Bytes) != "<a>é</a>" {
		t.Fatalf("binary receive ownership: value=%q log=%#v error=%v", text, log, err)
	}
	_, _, err = decode("<a>", 1, sqltype.XMLContent, sqltype.OutputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2200N" || diagnostic.Context != "unnamed portal parameter $1" {
		t.Fatalf("binary provenance=%#v error=%v", diagnostic, err)
	}
	_, _, err = decode("<a>", 0, sqltype.XMLContent, sqltype.OutputEnvironment{})
	if !errors.As(err, &diagnostic) || diagnostic.Context != "unnamed portal parameter $1 = '...'" {
		t.Fatalf("text provenance=%#v error=%v", diagnostic, err)
	}
	_, _, err = decode(`<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`, 0, sqltype.XMLDocument, sqltype.OutputEnvironment{XMLNotice: func(sqlstate.Error) { t.Error("parameter logging emitted typed output warning") }})
	if err != nil {
		t.Fatal(err)
	}
}
