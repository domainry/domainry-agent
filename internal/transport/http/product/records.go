package product

import (
	"encoding/json"
	"errors"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-knowledge/contract"
	sdk "github.com/domainry/domainry-tools-sdk"
	toolsmodule "github.com/domainry/domainry-tools/module"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// RecordRoutes shares browser transport only. Product adapters own validation,
// persisted data and their permission keys; Identity establishes request scope.
func RecordRoutes(runtime string, adapters ...*toolsmodule.RecordAdapter) http.Handler {
	catalog := map[string]*toolsmodule.RecordAdapter{}
	for _, a := range adapters {
		catalog[a.Spec.Kind] = a
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/app/product/records/"), "/")
		if len(parts) < 1 || len(parts) > 2 {
			http.NotFound(w, r)
			return
		}
		a := catalog[parts[0]]
		if a == nil {
			http.NotFound(w, r)
			return
		}
		identity, ok := identity.RequestIdentityFromContext(r.Context())
		if !ok || !identity.Principal.Known {
			productError(w, &sdk.Error{Class: "forbidden", Code: "product.principal_required"})
			return
		}
		auth := sdk.Authority{Known: true, RuntimeID: runtime, WorkspaceID: identity.Principal.WorkspaceID, UserID: identity.Principal.UserID, RoleKey: identity.Principal.RoleKey}
		op := "list"
		if len(parts) == 2 {
			op = "read"
		}
		if r.Method == "POST" && len(parts) == 1 {
			op = "save"
		} else if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var def sdk.Definition
		for _, d := range toolsmodule.RecordDefinitions(a.Spec) {
			if d.Key == a.Spec.Prefix+"_"+op {
				def = d
			}
		}
		decision, err := a.Authorize(r.Context(), sdk.Request{Authority: auth, Definition: def})
		if err != nil {
			productError(w, err)
			return
		}
		if !decision.Granted || decision.ConfirmationRequired {
			productError(w, &sdk.Error{Class: "forbidden", Code: "product.access_denied"})
			return
		}
		var value any
		switch op {
		case "list":
			limit := 5
			if raw := r.URL.Query().Get("limit"); raw != "" {
				limit, err = strconv.Atoi(raw)
				if err != nil {
					productError(w, &sdk.Error{Class: "bad_request", Code: "product.query_invalid"})
					return
				}
			}
			value, err = a.Store.List(r.Context(), a.Spec.Kind, r.URL.Query().Get("query"), r.URL.Query().Get("cursor"), limit, auth)
		case "read":
			revision := int64(0)
			if raw := r.URL.Query().Get("revision"); raw != "" {
				revision, err = strconv.ParseInt(raw, 10, 64)
				if err != nil {
					productError(w, &sdk.Error{Class: "bad_request", Code: "product.query_invalid"})
					return
				}
			}
			value, err = a.Store.Get(r.Context(), a.Spec.Kind, parts[1], revision, auth)
		case "save":
			var in contract.Write
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
				productError(w, &sdk.Error{Class: "bad_request", Code: "product.body_invalid"})
				return
			}
			value, err = a.Store.Save(r.Context(), a.Spec.Kind, in, auth, a.Spec.Validate)
		}
		if err != nil {
			productError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	})
}
func productError(w http.ResponseWriter, err error) {
	status, code := 500, "product.unavailable"
	var e *sdk.Error
	if errors.As(err, &e) {
		code = e.Code
		switch e.Class {
		case "bad_request":
			status = 400
		case "forbidden":
			status = 403
		case "not_found":
			status = 404
		case "conflict":
			status = 409
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
