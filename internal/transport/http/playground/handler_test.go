package playground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type localService struct {
	agentsdk.ConversationService
	authority agentsdk.ConversationAuthority
}

func (s *localService) List(_ context.Context, _ agentsdk.ConversationQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationPage, error) {
	s.authority = a
	return agentsdk.ConversationPage{Items: []agentsdk.Conversation{}}, nil
}

func TestLocalPlaygroundBoundary(t *testing.T) {
	service := &localService{}
	handler, err := NewHandler(service, "127.0.0.1:8090", "test-model", fstest.MapFS{"index.html": {Data: []byte("chat fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "http://127.0.0.1:8090/", nil))
	if page.Code != 200 || page.Body.String() != "chat fixture" {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}
	cookies := page.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("missing local session cookie")
	}
	for _, tc := range []struct {
		name, host, origin, site string
		cookie                   bool
		want                     int
	}{
		{"missing cookie", "127.0.0.1:8090", "", "", false, 403},
		{"wrong host", "evil.example", "", "", true, 403},
		{"cross origin", "127.0.0.1:8090", "https://evil.example", "", true, 403},
		{"cross site", "127.0.0.1:8090", "", "cross-site", true, 403},
		{"same origin", "127.0.0.1:8090", "http://127.0.0.1:8090", "same-origin", true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/agent/conversations", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			if tc.cookie {
				req.AddCookie(cookies[0])
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
	if !service.authority.Known || service.authority.RuntimeID != "agent-playground" || service.authority.UserID != "playground-user" {
		t.Fatalf("unexpected authority %+v", service.authority)
	}
}
