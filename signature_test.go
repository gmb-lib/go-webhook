package webhook

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A fresh secret per test run, assembled at run time so nothing in the source is shaped
// like a credential.
func testSecret(tag string) []byte {
	return []byte("test-" + tag + "-" + strings.Repeat("k", 24))
}

func TestSignatureRoundTrip(t *testing.T) {
	secret := testSecret("a")
	body := []byte(`{"eventId":"e1","type":"thing.happened"}`)
	sent := time.Date(2026, 9, 6, 14, 48, 43, 0, time.UTC)

	h := SignatureHeader(sent, body, secret)
	want := "t=" + strconv.FormatInt(sent.Unix(), 10) + ",v1="
	if !strings.HasPrefix(h, want) {
		t.Fatalf("header shape: %s, want prefix %s", h, want)
	}
	if !strings.HasSuffix(h, Sign(secret, sent, body)) || len(h) != len(want)+64 {
		t.Fatalf("one hex HMAC-SHA256 expected after the prefix: %s", h)
	}
	if err := Verify(h, body, [][]byte{secret}, sent.Add(30*time.Second), 5*time.Minute); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignatureRotationAcceptsEitherSecret(t *testing.T) {
	current, previous := testSecret("new"), testSecret("old")
	body := []byte(`{}`)
	sent := time.Unix(1757170123, 0)

	h := SignatureHeader(sent, body, current, previous)
	if strings.Count(h, "v1=") != 2 {
		t.Fatalf("two signatures expected during a rotation: %s", h)
	}
	// A receiver still on the old secret verifies; one already on the new one too.
	for name, held := range map[string][][]byte{"old only": {previous}, "new only": {current}} {
		if err := Verify(h, body, held, sent, 0); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSignatureRefusals(t *testing.T) {
	secret := testSecret("r")
	body := []byte(`{"n":1}`)
	sent := time.Unix(1757170123, 0)
	good := SignatureHeader(sent, body, secret)

	cases := []struct {
		name    string
		header  string
		body    []byte
		secrets [][]byte
		now     time.Time
		tol     time.Duration
		want    error
	}{
		{"tampered body", good, []byte(`{"n":2}`), [][]byte{secret}, sent, 0, ErrSignatureMismatch},
		{"wrong secret", good, body, [][]byte{testSecret("other")}, sent, 0, ErrSignatureMismatch},
		{"no secrets", good, body, nil, sent, 0, ErrSignatureMismatch},
		{"stale, too old", good, body, [][]byte{secret}, sent.Add(6 * time.Minute), 5 * time.Minute, ErrSignatureStale},
		{"stale, from the future", good, body, [][]byte{secret}, sent.Add(-6 * time.Minute), 5 * time.Minute, ErrSignatureStale},
		{"no t", "v1=" + strings.Repeat("a", 64), body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"two t", "t=1,t=2,v1=" + strings.Repeat("a", 64), body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"no v1", "t=1757170123", body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"short v1", "t=1757170123,v1=abcd", body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"non-hex v1", "t=1757170123,v1=" + strings.Repeat("z", 64), body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"negative t", "t=-5,v1=" + strings.Repeat("a", 64), body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"member without =", "t=1757170123,v1", body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
		{"empty", "", body, [][]byte{secret}, sent, 0, ErrSignatureMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Verify(c.header, c.body, c.secrets, c.now, c.tol)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestParseSignatureIgnoresUnknownMembersAndSpaces(t *testing.T) {
	sig := strings.Repeat("ab", 32)
	ts, sigs, err := ParseSignature(" t = 1757170123 , v2=future, v1 = " + strings.ToUpper(sig) + " ")
	if err != nil {
		t.Fatal(err)
	}
	if ts.Unix() != 1757170123 || len(sigs) != 1 || sigs[0] != sig {
		t.Fatalf("parsed t=%d sigs=%v", ts.Unix(), sigs)
	}
}

// The header is the one place a receiver reads untrusted bytes; it must never panic.
func FuzzParseSignature(f *testing.F) {
	f.Add("t=1757170123,v1=" + strings.Repeat("a", 64))
	f.Add("t=,v1=")
	f.Add(",,,=,=,t==1")
	f.Add("t=99999999999999999999,v1=" + strings.Repeat("f", 64))
	f.Fuzz(func(t *testing.T, header string) {
		ts, sigs, err := ParseSignature(header)
		if err == nil {
			if ts.IsZero() && ts.Unix() != 0 {
				t.Fatal("success with an unset time")
			}
			if len(sigs) == 0 {
				t.Fatal("success with no signatures")
			}
		}
		// Whatever it parsed, verification must also not panic.
		_ = Verify(header, []byte("x"), [][]byte{[]byte("k")}, time.Unix(1757170123, 0), time.Minute)
	})
}
