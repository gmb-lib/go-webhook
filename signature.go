package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// The signature scheme, as a receiver sees it.
//
// A delivery carries one header of the form
//
//	t=1757170123,v1=5f1a…,v1=9c02…
//
// where t is the send time in Unix seconds and each v1 is the lowercase hex
// HMAC-SHA256, keyed with one of the subscription's active secrets, over the bytes
//
//	<t> "." <raw request body>
//
// Two v1 values appear while a secret rotation is in progress; a receiver accepts the
// delivery if any one of them matches a key it holds. Binding t into the signed bytes
// lets the receiver refuse a replayed delivery once t is outside its tolerance window.

// Errors a receiver's verification returns. Compare with [errors.Is].
var (
	ErrSignatureMalformed = errors.New("webhook: signature header malformed")
	ErrSignatureStale     = errors.New("webhook: signature timestamp outside the tolerance window")
	ErrSignatureMismatch  = errors.New("webhook: signature does not match any known secret")
)

// Sign returns the lowercase hex HMAC-SHA256 of `<t> "." <body>` under secret, t being
// Unix seconds.
func Sign(secret []byte, t time.Time, body []byte) string {
	return signUnix(secret, t.Unix(), body)
}

func signUnix(secret []byte, unix int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(unix, 10)))
	mac.Write([]byte("."))
	mac.Write(body)

	return hex.EncodeToString(mac.Sum(nil))
}

// SignatureHeader builds the header value for a delivery sent at t: `t=<unix>` followed
// by one `v1=<hex>` per secret, in the order given (current first).
func SignatureHeader(t time.Time, body []byte, secrets ...[]byte) string {
	var b strings.Builder
	b.WriteString("t=")
	b.WriteString(strconv.FormatInt(t.Unix(), 10))
	for _, s := range secrets {
		b.WriteString(",v1=")
		b.WriteString(Sign(s, t, body))
	}

	return b.String()
}

// ParseSignature splits a header value into its timestamp and its v1 signatures. It
// tolerates spaces around separators and ignores members it does not know (a later
// scheme version beside v1), but requires exactly one t and at least one v1, each
// well-formed. This is the one place untrusted header bytes are read, so it is
// deliberately strict and never panics.
func ParseSignature(header string) (t time.Time, signatures []string, err error) {
	var (
		unix    int64
		haveT   bool
		members = strings.Split(header, ",")
	)
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		k, v, ok := strings.Cut(m, "=")
		if !ok {
			return time.Time{}, nil, ErrSignatureMalformed
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "t":
			if haveT {
				return time.Time{}, nil, ErrSignatureMalformed
			}
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil || n < 0 {
				return time.Time{}, nil, ErrSignatureMalformed
			}
			unix, haveT = n, true
		case "v1":
			if len(v) != sha256.Size*2 {
				return time.Time{}, nil, ErrSignatureMalformed
			}
			if _, herr := hex.DecodeString(v); herr != nil {
				return time.Time{}, nil, ErrSignatureMalformed
			}
			signatures = append(signatures, strings.ToLower(v))
		default:
			// An unknown member is a scheme this version does not know; ignore it so a
			// receiver on this version keeps working when a v2 is added beside v1.
		}
	}
	if !haveT || len(signatures) == 0 {
		return time.Time{}, nil, ErrSignatureMalformed
	}

	return time.Unix(unix, 0).UTC(), signatures, nil
}

// Verify is what a receiver runs on each delivery: parse the header, refuse a
// timestamp further than tolerance from now (a replay, or a clock nobody trusts), then
// accept if any v1 matches the HMAC under any of the receiver's secrets. Comparison is
// constant-time. A tolerance of zero disables the window check.
func Verify(header string, body []byte, secrets [][]byte, now time.Time, tolerance time.Duration) error {
	t, sigs, err := ParseSignature(header)
	if err != nil {
		return err
	}
	if tolerance > 0 {
		d := now.Sub(t)
		if d < 0 {
			d = -d
		}
		if d > tolerance {
			return ErrSignatureStale
		}
	}
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		want := []byte(signUnix(secret, t.Unix(), body))
		for _, got := range sigs {
			if hmac.Equal(want, []byte(got)) {
				return nil
			}
		}
	}

	return ErrSignatureMismatch
}
