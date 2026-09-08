package upstream

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// Keep the User-Agent and client hints aligned with the explicit TLS profile.
const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

type browserTransport struct {
	client tlsclient.HttpClient
}

func newBrowserTransport(timeout time.Duration, proxyURL string, extraOptions ...tlsclient.HttpClientOption) (*browserTransport, error) {
	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(profiles.Chrome_150),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithDisableHttp3(),
		// The outer net/http.Client owns redirects and the total timeout.
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithTimeoutMilliseconds(int(timeout.Milliseconds())),
	}
	if proxyURL != "" {
		options = append(options, tlsclient.WithProxyUrl(proxyURL))
	}
	options = append(options, extraOptions...)
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, &Error{Code: NetworkError, Message: "无法创建浏览器请求客户端，请检查代理配置", Cause: err}
	}
	return &browserTransport{client: client}, nil
}

func (t *browserTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body := request.Body
	if body == http.NoBody {
		body = nil
	}
	upstreamRequest, err := fhttp.NewRequestWithContext(request.Context(), request.Method, request.URL.String(), body)
	if err != nil {
		if request.Body != nil {
			request.Body.Close()
		}
		return nil, err
	}
	upstreamRequest.Host = request.Host
	upstreamRequest.ContentLength = request.ContentLength
	upstreamRequest.GetBody = request.GetBody
	upstreamRequest.Close = request.Close
	upstreamRequest.TransferEncoding = append([]string(nil), request.TransferEncoding...)
	upstreamRequest.Trailer = fhttp.Header(request.Trailer.Clone())
	for name, values := range request.Header {
		key := strings.ToLower(name)
		upstreamRequest.Header[key] = append(upstreamRequest.Header[key], values...)
	}
	setDefault := func(name, value string) {
		if _, exists := upstreamRequest.Header[name]; !exists {
			upstreamRequest.Header[name] = []string{value}
		}
	}
	setDefault("user-agent", browserUserAgent)
	setDefault("sec-ch-ua", `"Chromium";v="150", "Google Chrome";v="150", "Not_A Brand";v="99"`)
	setDefault("sec-ch-ua-mobile", "?0")
	setDefault("sec-ch-ua-platform", `"Windows"`)
	setDefault("accept", "application/json")
	setDefault("accept-language", "en-US,en;q=0.9")
	autoDecompress := request.Header.Get("Accept-Encoding") == "" && request.Header.Get("Range") == "" && request.Method != http.MethodHead
	if autoDecompress {
		setDefault("accept-encoding", "gzip, deflate, br, zstd")
	}
	upstreamRequest.Header[fhttp.HeaderOrderKey] = []string{
		"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "user-agent",
		"accept", "authorization", "origin", "sec-fetch-site", "sec-fetch-mode",
		"sec-fetch-dest", "referer", "accept-encoding", "accept-language", "cookie", "priority",
	}
	response, err := t.client.Do(upstreamRequest)
	if err != nil {
		return nil, err
	}
	// fhttp's HTTP/1 and HTTP/2 transports differ in when they decompress an
	// explicitly advertised encoding. Normalize that behavior for JSON callers.
	if autoDecompress && !response.Uncompressed {
		switch response.Header.Get("Content-Encoding") {
		case "gzip", "deflate", "br", "zstd":
			response.Body = fhttp.DecompressBody(response)
		}
	}
	headers := make(http.Header, len(response.Header))
	for name, values := range response.Header {
		key := http.CanonicalHeaderKey(name)
		headers[key] = append(headers[key], values...)
	}
	if response.Uncompressed {
		headers.Del("Content-Encoding")
		headers.Del("Content-Length")
	}
	result := &http.Response{
		Status: response.Status, StatusCode: response.StatusCode,
		Proto: response.Proto, ProtoMajor: response.ProtoMajor, ProtoMinor: response.ProtoMinor,
		Header: headers, Body: response.Body, ContentLength: response.ContentLength,
		TransferEncoding: response.TransferEncoding, Close: response.Close,
		Uncompressed: response.Uncompressed, Trailer: http.Header(response.Trailer), Request: request,
	}
	if state := response.TLS; state != nil {
		result.TLS = &tls.ConnectionState{
			Version: state.Version, HandshakeComplete: state.HandshakeComplete, DidResume: state.DidResume,
			CipherSuite: state.CipherSuite, NegotiatedProtocol: state.NegotiatedProtocol,
			NegotiatedProtocolIsMutual: state.NegotiatedProtocolIsMutual, ServerName: state.ServerName,
			PeerCertificates: state.PeerCertificates, VerifiedChains: state.VerifiedChains,
			SignedCertificateTimestamps: state.SignedCertificateTimestamps, OCSPResponse: state.OCSPResponse,
			TLSUnique: state.TLSUnique, ECHAccepted: state.ECHAccepted,
		}
	}
	return result, nil
}

func (t *browserTransport) CloseIdleConnections() { t.client.CloseIdleConnections() }

var _ http.RoundTripper = (*browserTransport)(nil)
