package web

import (
	"context"
	"fmt"
	connector "github.com/domainry/domainry-connector-sdk"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

type accountProviderHTTPTransport struct {
	target       *httptest.Server
	host, prefix string
}

func (tr accountProviderHTTPTransport) ExecuteSQL(context.Context, connector.SQLRequest) (connector.SQLResult, error) {
	return connector.SQLResult{}, fmt.Errorf("unexpected SQL")
}
func (tr accountProviderHTTPTransport) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" || u.Host != tr.host || !strings.HasPrefix(u.Path, tr.prefix) {
		return connector.HTTPResponse{}, fmt.Errorf("unexpected account provider endpoint")
	}
	r, err := http.NewRequestWithContext(ctx, in.Method, tr.target.URL+u.RequestURI(), strings.NewReader(string(in.Body)))
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	for key, values := range in.Headers {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	for key, values := range in.SecretHeaders {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	out, err := tr.target.Client().Do(r)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	defer out.Body.Close()
	limit := in.MaxResponseBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	b, err := io.ReadAll(io.LimitReader(out.Body, int64(limit)+1))
	if int64(len(b)) > limit {
		return connector.HTTPResponse{}, fmt.Errorf("response too large")
	}
	return connector.HTTPResponse{StatusCode: out.StatusCode, Headers: out.Header, Body: b}, err
}
