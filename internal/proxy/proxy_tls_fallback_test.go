package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDoRequestRefusesCertificateFailureByDefault pins the safe default: a
// certificate that does not verify fails the request. It used to be replayed
// with verification disabled unless STRICT_SSL was set, which shipped a
// silent MITM window to every install that never read the docs.
func TestDoRequestRefusesCertificateFailureByDefault(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TLS", "")
	var hits atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, err := DoRequest(context.Background(), &http.Client{}, http.MethodPost, upstream.URL, nil, []byte("`{}/*LB*/"))
	if err == nil {
		t.Fatal("a self-signed upstream must fail verification by default")
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("error = %v, want the certificate verification failure", err)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want no retry without verification", hits.Load())
	}
}

// TestDoRequestRetriesWithInsecureTLS runs the corporate middlebox case — a
// proxy that re-signs TLS with a root no public store vouches for — but only
// once the operator has opted in by name.
func TestDoRequestRetriesWithInsecureTLS(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TLS", "true")
	t.Setenv("ALLOW_PRIVATE_NETWORKING", "true")

	var hits atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	resp, err := DoRequest(context.Background(), &http.Client{}, http.MethodPost, upstream.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("a self-signed upstream must be retried without certificate verification: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want the insecure retry to have reached it", hits.Load())
	}
}

// TestDoRequestInsecureOptInOffRefusesRetry pins that anything but an explicit
// true/1 leaves the safe default on: the certificate error has to surface
// rather than being swallowed by a different failure.
func TestDoRequestInsecureOptInOffRefusesRetry(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TLS", "false")

	var hits atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, err := DoRequest(context.Background(), &http.Client{}, http.MethodPost, upstream.URL, nil, []byte(`{}`))
	if err == nil {
		t.Fatal("without the opt-in the request must fail instead of retrying without verification")
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want no insecure attempt to have been made", hits.Load())
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("error = %v, want the certificate verification failure", err)
	}
}

// TestDoRequestDoesNotMaskCertificateErrorAsProxyFailure guards the detection
// method. isProxyFailure matches error text, so a certificate failure could be
// swallowed by the direct replay and reported as an ordinary proxy refusal;
// the typed check has to keep the two apart.
func TestDoRequestDoesNotMaskCertificateErrorAsProxyFailure(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TLS", "1")

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, err := DoRequest(context.Background(), &http.Client{}, http.MethodPost, upstream.URL, nil, []byte(`{}`))
	if err == nil {
		t.Fatal("expected the certificate failure")
	}
	if strings.Contains(err.Error(), "retrying direct") {
		t.Errorf("a certificate failure must not be replayed direct: %v", err)
	}
}

// TestInsecureClientForCachesAndSkipsVerification pins the cache: the retry
// transport is shared per proxy URL, and it really does skip verification.
func TestInsecureClientForCachesAndSkipsVerification(t *testing.T) {
	first := InsecureClientFor("http://127.0.0.1:1")
	second := InsecureClientFor("http://127.0.0.1:1")
	if first != second {
		t.Error("insecure clients must be cached per proxy url, got a fresh client each call")
	}
	tr, ok := first.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", first.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("the cached transport must disable certificate verification")
	}
	if tr.Proxy == nil {
		t.Error("the cached transport must dial the given proxy")
	}
	if InsecureClientFor("") == first {
		t.Error("the direct insecure client must not share a pool with a proxied one")
	}
}

// TestIsCertificateFailure covers the typed detection directly, including the
// error classes the retry must not claim.
func TestIsCertificateFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no error", err: nil, want: false},
		{name: "cancelled context", err: context.Canceled, want: false},
		{name: "proxy refusal mentions proxy", err: errProxyRefusal, want: false},
		{
			name: "unknown authority",
			err:  &wrappedErr{err: x509.UnknownAuthorityError{}},
			want: true,
		},
		{
			name: "expired certificate",
			err:  &wrappedErr{err: x509.CertificateInvalidError{Reason: x509.Expired}},
			want: true,
		},
		{
			name: "hostname mismatch",
			err:  &wrappedErr{err: x509.HostnameError{Host: "upstream.invalid"}},
			want: true,
		},
		{
			name: "handshake verification wrapper",
			err:  &wrappedErr{err: &tls.CertificateVerificationError{}},
			want: true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCertificateFailure(tt.err); got != tt.want {
				t.Errorf("isCertificateFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

// errProxyRefusal carries the text a proxy refusal produces, which
// isProxyFailure keys on. A certificate failure must never be routed by it.
var errProxyRefusal = &wrappedErr{err: errString("proxyconnect tcp: connection refused")}

type errString string

func (e errString) Error() string { return string(e) }

// wrappedErr stands in for the *url.Error the stdlib wraps transport failures
// in, which is what errors.As has to see through in production.
type wrappedErr struct{ err error }

func (e *wrappedErr) Error() string { return `Post "https://upstream.invalid": ` + e.err.Error() }

func (e *wrappedErr) Unwrap() error { return e.err }

// TestInsecureTLSOptInFollowsEnv pins the opt-in reading. It is read per
// request rather than at init, so a flipped variable takes effect without a
// new process.
func TestInsecureTLSOptInFollowsEnv(t *testing.T) {
	cases := map[string]bool{
		"":       false,
		"0":      false,
		"false":  false,
		"no":     false,
		"true":   true,
		" TRUE ": true,
		"1":      true,
	}
	for value, want := range cases {
		t.Run("ALLOW_INSECURE_TLS="+value, func(t *testing.T) {
			t.Setenv("ALLOW_INSECURE_TLS", value)
			if got := insecureTLSOptIn(); got != want {
				t.Errorf("insecureTLSOptIn() = %v, want %v", got, want)
			}
		})
	}
}