package sqltype

import (
	"context"
	"testing"
)

func TestTimeOptionalDateCompletion(t *testing.T) {
	d := timeDescriptor(t)
	for _, text := range []string{"2003.032 04:05 Etc/UTC", "2000.366 04:05 Etc/UTC", "1999.366 04:05 Etc/UTC", "07-Mar-2003 04:05", "2003-07-07 04:05:06+02"} {
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(text, err)
		}
		out, err := d.Output(context.Background(), v)
		want := "04:05:00"
		if text == "2003-07-07 04:05:06+02" {
			want = "04:05:06"
		}
		if err != nil || out != want {
			t.Fatal(text, out, err)
		}
	}
	for _, text := range []string{"2003.032 04:05", "2003-07-07T04:05", "04:05 2003-07-07 America/New_York", "2003-07-07 0405", "040506 2003-07-07", "j2451545.5 04:05", "j2451545.5 j2451545.5"} {
		_, err := d.Input(context.Background(), text, -1)
		if err == nil {
			t.Fatal("incomplete/duplicate fields accepted", text)
		}
	}
}
