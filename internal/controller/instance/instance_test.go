/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package instance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"

	v1alpha1 "github.com/garyleungsky/provider-learn/apis/database/v1alpha1"
)

// These tests stand up an httptest.Server that mimics hack/mock-apiserver's
// /v1/instances contract, so the controller is exercised against the real HTTP
// client end to end. The mock server lives in a separate Go module and so is
// reproduced here rather than imported.

// newTestServer returns a server backed by an in-memory store mirroring the
// mock-apiserver contract.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := map[string]apiInstance{}
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		var in apiInstance
		_ = json.NewDecoder(r.Body).Decode(&in)
		if _, ok := store[in.Name]; ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		in.ObservableField = in.Name + ".instances.mock.local"
		store[in.Name] = in
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	})

	mux.HandleFunc("/v1/instances/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
		existing, ok := store[name]
		switch r.Method {
		case http.MethodGet:
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(existing)
		case http.MethodPut:
			var in apiInstance
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Name != name {
				t.Errorf("PUT body name = %q, want %q", in.Name, name)
			}
			existing.ConfigurableField = in.ConfigurableField
			store[name] = existing
			_ = json.NewEncoder(w).Encode(existing)
		case http.MethodDelete:
			delete(store, name)
			w.WriteHeader(http.StatusNoContent)
		}
	})

	return httptest.NewServer(mux)
}

func instanceWith(externalName, configurable string) *v1alpha1.Instance {
	cr := &v1alpha1.Instance{}
	meta.SetExternalName(cr, externalName)
	cr.Spec.ForProvider.ConfigurableField = configurable
	return cr
}

// TestExternalLifecycle walks a managed resource through its full reconcile
// lifecycle: absent -> Create -> up to date -> drift -> Update -> Delete.
func TestExternalLifecycle(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	e := &external{client: &apiClient{baseURL: srv.URL, http: srv.Client()}}
	ctx := context.Background()
	cr := instanceWith("db1", "small")

	// 1. Before creation, Observe reports the resource does not exist.
	if obs, err := e.Observe(ctx, cr); err != nil || obs.ResourceExists {
		t.Fatalf("Observe(absent): exists=%v err=%v", obs.ResourceExists, err)
	}

	// 2. Create provisions it; the server computes observableField.
	creation, err := e.Create(ctx, cr)
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if got := string(creation.ConnectionDetails["observableField"]); got != "db1.instances.mock.local" {
		t.Fatalf("Create: observableField=%q", got)
	}

	// 3. Observe now finds it, up to date, and populates status.atProvider.
	obs, err := e.Observe(ctx, cr)
	if err != nil || !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("Observe(present): exists=%v upToDate=%v err=%v", obs.ResourceExists, obs.ResourceUpToDate, err)
	}
	if cr.Status.AtProvider.ObservableField != "db1.instances.mock.local" {
		t.Fatalf("status.atProvider.observableField=%q", cr.Status.AtProvider.ObservableField)
	}

	// 4. Changing the spec makes Observe report drift.
	cr.Spec.ForProvider.ConfigurableField = "large"
	if obs, _ := e.Observe(ctx, cr); obs.ResourceUpToDate {
		t.Fatalf("Observe(drift): want ResourceUpToDate=false")
	}

	// 5. Update reconciles the drift.
	if _, err := e.Update(ctx, cr); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if obs, _ := e.Observe(ctx, cr); !obs.ResourceUpToDate {
		t.Fatalf("Observe(after update): want ResourceUpToDate=true")
	}

	// 6. Delete removes it; Observe reports it gone.
	if _, err := e.Delete(ctx, cr); err != nil {
		t.Fatalf("Delete: unexpected error: %v", err)
	}
	if obs, _ := e.Observe(ctx, cr); obs.ResourceExists {
		t.Fatalf("Observe(after delete): want ResourceExists=false")
	}
}

// TestObserveNoExternalName verifies Observe short-circuits (no HTTP call) when
// the resource has no external name yet.
func TestObserveNoExternalName(t *testing.T) {
	e := &external{client: &apiClient{baseURL: "http://127.0.0.1:0"}}
	obs, err := e.Observe(context.Background(), &v1alpha1.Instance{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceExists {
		t.Fatalf("want ResourceExists=false for empty external-name")
	}
}
