package upstream

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	tlsclient "github.com/bogdanfinn/tls-client"
)

func TestBrowserTransportHTTPS(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, encoding := range []string{"gzip", "br"} {
			t.Run("http2="+strconv.FormatBool(http2)+"/"+encoding, func(t *testing.T) {
				var browserHello atomic.Bool
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					account := r.URL.Query().Get("account")
					body, _ := io.ReadAll(r.Body)
					if r.Method != http.MethodPost || string(body) != "payload" || r.ContentLength != 7 {
						t.Error("request method, body or content length was lost")
					}
					if r.Header.Get("Authorization") != "Bearer "+account || r.Header.Get("Cookie") != "session="+account {
						t.Error("credentials were lost or shared between accounts")
					}
					if !strings.Contains(r.UserAgent(), "Chrome/150.") || !strings.Contains(r.Header.Get("Sec-CH-UA"), `v="150"`) {
						t.Error("browser headers do not match the Chrome profile")
					}
					if r.Header.Get("X-Custom") != "retained" || r.Header.Get("Proxy-Authorization") != "" {
						t.Error("custom headers were lost or proxy credentials reached the origin")
					}
					http.SetCookie(w, &http.Cookie{Name: "__Secure-next-auth.session-token.0", Value: "rotated-"})
					http.SetCookie(w, &http.Cookie{Name: "__Secure-next-auth.session-token.1", Value: account})
					w.Header().Set("X-Upstream", "retained")
					w.Header().Set("Content-Encoding", encoding)
					w.WriteHeader(http.StatusAccepted)
					var writer io.WriteCloser
					if encoding == "gzip" {
						writer = gzip.NewWriter(w)
					} else {
						writer = brotli.NewWriter(w)
					}
					_, _ = io.WriteString(writer, `{"ok":true}`)
					_ = writer.Close()
				}))
				server.EnableHTTP2 = http2
				server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					// Chrome sends a GREASE cipher suite before its actual suites;
					// Go's default TLS handshake does not. Check the wire handshake.
					if len(hello.CipherSuites) > 0 {
						first := hello.CipherSuites[0]
						browserHello.Store(first&0x0f0f == 0x0a0a && first>>8 == first&0xff)
					}
					return nil, nil
				}}
				server.StartTLS()
				defer server.Close()
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				transport, err := newBrowserTransport(2*time.Second, "", tlsclient.WithTransportOptions(&tlsclient.TransportOptions{RootCAs: roots}))
				if err != nil {
					t.Fatal(err)
				}
				client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
				defer client.CloseIdleConnections()
				for _, account := range []string{"account-a", "account-b"} {
					request, _ := http.NewRequest(http.MethodPost, server.URL+"?account="+account, strings.NewReader("payload"))
					request.Header.Set("Authorization", "Bearer "+account)
					request.Header.Set("Cookie", "session="+account)
					request.Header.Set("X-Custom", "retained")
					originalHeaders := request.Header.Clone()
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(response.Body)
					response.Body.Close()
					if readErr != nil || string(body) != `{"ok":true}` {
						t.Fatalf("compressed body was not decoded: body=%q err=%v", body, readErr)
					}
					wantProtocol := 1
					if http2 {
						wantProtocol = 2
					}
					if response.ProtoMajor != wantProtocol || response.StatusCode != http.StatusAccepted || response.Header.Get("X-Upstream") != "retained" {
						t.Fatalf("response metadata was lost: protocol=%s status=%d", response.Proto, response.StatusCode)
					}
					if !response.Uncompressed || response.Header.Get("Content-Encoding") != "" || response.ContentLength != -1 {
						t.Fatal("decompressed response retained compressed metadata")
					}
					cookies := response.Cookies()
					if len(cookies) != 2 || cookies[0].Value != "rotated-" || cookies[1].Value != account {
						t.Fatal("rotated session cookies were lost")
					}
					// fhttp exposes connection state on HTTP/2 responses. Its
					// custom HTTP/1 TLS dialer leaves this optional field nil.
					if http2 && (response.TLS == nil || len(response.TLS.VerifiedChains) == 0) {
						t.Fatal("verified TLS connection details were lost")
					}
					if !reflect.DeepEqual(request.Header, originalHeaders) {
						t.Fatal("transport modified the caller's headers")
					}
				}
				if !browserHello.Load() {
					t.Fatal("request did not use a browser TLS ClientHello")
				}
			})
		}
	}
}

func TestBrowserTransportRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	transport, err := newBrowserTransport(2*time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("browser client accepted an untrusted certificate")
	}
}

func TestBrowserClientRetriesAndHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := testClient(t, time.Second, 1)
	client.browser = true
	defer client.CloseIdleConnections()
	response, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Fatalf("retry or body lifetime changed: calls=%d err=%v", calls.Load(), readErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response, err = client.Do(ctx, func(_ *http.Client) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL+"/slow", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, readErr = io.ReadAll(response.Body)
	response.Body.Close()
	if readErr == nil {
		t.Fatal("response body ignored request cancellation")
	}
}

func TestBrowserClientProxySettings(t *testing.T) {
	var origins, tunnels atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached the origin")
		}
		_, _ = io.WriteString(w, "origin")
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != strings.TrimPrefix(origin.URL, "http://") {
			t.Error("unexpected proxy target")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password")) {
			t.Error("proxy authentication was lost")
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		upstreamConn, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstreamConn.Close()
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		tunnels.Add(1)
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { _, _ = io.Copy(upstreamConn, buffered) }()
		_, _ = io.Copy(connection, upstreamConn)
	}))
	defer proxy.Close()
	// An unset application proxy must not inherit the host's proxy settings.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client := testClient(t, 2*time.Second, 0)
	client.browser = true
	defer client.CloseIdleConnections()
	for _, enabled := range []bool{false, true, false} {
		proxyURL := strings.Replace(proxy.URL, "http://", "http://proxy-user:proxy-password@", 1)
		if _, err := client.settings.Update(context.Background(), map[string]any{"proxy_enabled": enabled, "proxy_url": proxyURL}); err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
			return http.NewRequest(http.MethodGet, origin.URL, nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(body, []byte("origin")) {
			t.Fatalf("proxy response: %q err=%v", body, err)
		}
	}
	if origins.Load() != 3 || tunnels.Load() != 1 {
		t.Fatalf("proxy setting changes were not applied: origins=%d tunnels=%d", origins.Load(), tunnels.Load())
	}
}

func TestBrowserClientTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testClient(t, 50*time.Millisecond, 2)
	client.browser = true
	defer client.CloseIdleConnections()
	_, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if Code(err) != Timeout {
		t.Fatalf("timeout classification changed: code=%s err=%v", Code(err), err)
	}
}

func TestBrowserTransportHonorsOuterRedirectPolicy(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()
	transport, err := newBrowserTransport(time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || calls.Load() != 1 {
		t.Fatal("inner TLS client followed a redirect despite the outer policy")
	}
}
