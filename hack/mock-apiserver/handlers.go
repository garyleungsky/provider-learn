package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// handleCollection serves the collection endpoint /v1/instances:
//   - GET  lists every instance.
//   - POST creates an instance; the server computes ObservableField.
func (s *store) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]instance, 0, len(s.instances))
		for _, in := range s.instances {
			out = append(out, in)
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var in instance
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
			writeJSON(w, http.StatusBadRequest, errBody("invalid body: name is required"))
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.instances[in.Name]; ok {
			writeJSON(w, http.StatusConflict, errBody("instance already exists"))
			return
		}
		// The server "provisions" the resource and assigns a read-only field.
		in.ObservableField = fmt.Sprintf("%s.instances.mock.local", in.Name)
		s.instances[in.Name] = in
		writeJSON(w, http.StatusCreated, in)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
	}
}

// handleItem serves the item endpoint /v1/instances/{name}:
//   - GET    returns one instance (404 if absent).
//   - PUT    updates ConfigurableField (404 if absent).
//   - DELETE removes the instance (404 if absent).
func (s *store) handleItem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, errBody("missing instance name"))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.instances[name]

	switch r.Method {
	case http.MethodGet:
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("instance not found"))
			return
		}
		writeJSON(w, http.StatusOK, existing)
	case http.MethodPut:
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("instance not found"))
			return
		}
		var in instance
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid body"))
			return
		}
		existing.ConfigurableField = in.ConfigurableField
		s.instances[name] = existing
		writeJSON(w, http.StatusOK, existing)
	case http.MethodDelete:
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("instance not found"))
			return
		}
		delete(s.instances, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
	}
}
