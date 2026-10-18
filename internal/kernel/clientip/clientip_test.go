package clientip

import "testing"

func TestClient(t *testing.T) {
	r, err := New("127.0.0.1, 172.16.0.0/12 ::1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		remote    string
		forwarded []string
		want      string
	}{
		// Not behind a trusted proxy: the header is anyone's to send.
		{"203.0.113.5:4000", []string{"198.51.100.1"}, "203.0.113.5"},
		{"203.0.113.5:4000", nil, "203.0.113.5"},
		// Behind one: the address it heard from.
		{"127.0.0.1:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"[::1]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		// What a client put first is skipped for the last untrusted hop.
		{"127.0.0.1:4000", []string{"10.9.9.9, 198.51.100.1, 172.18.0.2"}, "198.51.100.1"},
		{"172.18.0.3:4000", []string{"10.9.9.9", "198.51.100.1, 172.18.0.2"}, "198.51.100.1"},
		// A proxy without the header, or a malformed one: the last good hop.
		{"127.0.0.1:4000", nil, "127.0.0.1"},
		{"127.0.0.1:4000", []string{"bogus, 198.51.100.1"}, "198.51.100.1"},
		{"127.0.0.1:4000", []string{"198.51.100.1, bogus"}, "127.0.0.1"},
		// Every hop trusted: the first.
		{"127.0.0.1:4000", []string{"172.18.0.9, 172.18.0.2"}, "172.18.0.9"},
		{"::ffff:127.0.0.1", []string{"::ffff:198.51.100.1"}, "198.51.100.1"},
	} {
		if got := r.Client(c.remote, c.forwarded); got != c.want {
			t.Errorf("%s %v = %s, want %s", c.remote, c.forwarded, got, c.want)
		}
	}

	none, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if got := none.Client("127.0.0.1:4000", []string{"198.51.100.1"}); got != "127.0.0.1" {
		t.Errorf("trusting none: %s", got)
	}
	for _, spec := range []string{"localhost", "10.0.0.0/33", "1.2.3"} {
		if _, err := New(spec); err == nil {
			t.Errorf("%q: no error", spec)
		}
	}
}
