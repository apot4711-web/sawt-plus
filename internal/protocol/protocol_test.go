package protocol

import "testing"

func TestRoundTrip(t *testing.T) {
	in := Hello("secret", "test/1")
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "hello" || out.Token != "secret" || out.Client != "test/1" || out.Version != Version {
		t.Fatalf("unexpected message: %+v", out)
	}
}
