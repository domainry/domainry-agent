// Package connectortransport supplies the standalone Agent host's bounded
// outbound capability. Runtime embeddings can instead inject their Transport.
package connectortransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
)

type HTTP struct {
	origin       *url.URL
	client       *http.Client
	documentPath string
}

// NewKnowledgeDocumentHTTP explicitly adds bounded document management for one
// dedicated KB. The ordinary retrieval transport does not gain write access.
func NewKnowledgeDocumentHTTP(origin, kb string, client *http.Client) (*HTTP, error) {
	if kb == "" || len(kb) > 256 || kb == "." || kb == ".." || strings.ContainsAny(kb, "/\\\x00") {
		return nil, errors.New("knowledge document transport requires a valid KB ID")
	}
	t, err := NewHTTP(origin, client)
	if err != nil {
		return nil, err
	}
	t.documentPath = "/v1/kb/kbs/" + url.PathEscape(kb) + "/documents"
	return t, nil
}

func NewHTTP(origin string, client *http.Client) (*HTTP, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("connector transport requires a service origin")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, errors.New("connector transport requires HTTPS or loopback HTTP")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTP{origin: u, client: &bounded}, nil
}

func (t *HTTP) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != t.origin.Scheme || !strings.EqualFold(u.Host, t.origin.Host) || u.User != nil || u.Fragment != "" || in.MaxResponseBytes < 1 || in.MaxResponseBytes > 512*1024 || len(in.SecretQuery)+len(in.SecretForm)+len(in.SecretJSON) != 0 {
		return connector.HTTPResponse{}, errors.New("connector request exceeds host transport policy")
	}
	read := in.Method == http.MethodPost && (u.EscapedPath() == "/v1/kb/search" || u.EscapedPath() == "/v1/kb/fetch") && u.RawQuery == "" && !u.ForceQuery && len(in.Body) <= 128*1024
	write := false
	if t.documentPath != "" && u.EscapedPath() == t.documentPath {
		q, queryErr := url.ParseQuery(u.RawQuery)
		if queryErr == nil && len(q["doc_id"]) == 1 && strings.TrimSpace(q.Get("doc_id")) != "" && len(q.Get("doc_id")) <= 4096 {
			write = in.Method == http.MethodDelete && len(q) == 1 && len(in.Body) == 0 || in.Method == http.MethodPost && len(q) == 2 && len(q["filename"]) == 1 && q.Get("filename") != "" && len(q.Get("filename")) <= 255 && len(in.Body) > 0 && len(in.Body) <= 16<<20
		}
	}
	if !read && !write {
		return connector.HTTPResponse{}, errors.New("connector operation exceeds host transport policy")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, bytes.NewReader(in.Body))
	if err != nil {
		return connector.HTTPResponse{}, errors.New("invalid connector request")
	}
	for key, values := range in.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for key, values := range in.SecretHeaders {
		if len(req.Header.Values(key)) != 0 {
			return connector.HTTPResponse{}, errors.New("secret header collides with public header")
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	response, err := t.client.Do(req)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	defer response.Body.Close()
	// Return at most limit+1 so the Provider can classify oversized JSON without
	// retaining an unbounded body. HTTP failures never need their response body.
	var body []byte
	if response.StatusCode/100 == 2 {
		body, err = io.ReadAll(io.LimitReader(response.Body, in.MaxResponseBytes+1))
		if err != nil {
			return connector.HTTPResponse{}, err
		}
	}
	return connector.HTTPResponse{StatusCode: response.StatusCode, Body: body}, nil
}

func (*HTTP) ExecuteSQL(context.Context, connector.SQLRequest) (connector.SQLResult, error) {
	return connector.SQLResult{}, errors.New("SQL is not enabled for this connector transport")
}
