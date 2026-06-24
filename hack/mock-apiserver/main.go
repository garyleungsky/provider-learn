// Command mock-apiserver is a tiny in-memory REST API that stands in for the
// external system the Crossplane provider manages. It exposes CRUD over
// /v1/instances and logs every request and response to stdout, so you can watch
// the HTTP traffic the provider generates while it reconciles an Instance.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// instance is the external resource managed by the provider. It mirrors the
// Instance CRD: ConfigurableField is client-settable (forProvider), while
// ObservableField is server-computed and read back into atProvider.
type instance struct {
	Name              string `json:"name"`
	ConfigurableField string `json:"configurableField"`
	ObservableField   string `json:"observableField"`
}

// store is a concurrency-safe in-memory collection of instances keyed by name.
type store struct {
	mu        sync.Mutex
	instances map[string]instance
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	s := &store{instances: map[string]instance{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", s.handleCollection)
	mux.HandleFunc("/v1/instances/", s.handleItem)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("mock-apiserver listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, logging(mux)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

// logging is middleware that prints each request (method, path, body) and its
// response (status, body, latency) to stdout.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody []byte
		if r.Body != nil {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}
		log.Printf("--> %s %s %s", r.Method, r.URL.Path, strings.TrimSpace(string(reqBody)))

		rec := &recorder{ResponseWriter: w, status: http.StatusOK, buf: &bytes.Buffer{}}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Printf("<-- %d %s (%s) %s", rec.status, http.StatusText(rec.status),
			time.Since(start).Round(time.Millisecond), strings.TrimSpace(rec.buf.String()))
	})
}

// recorder captures the status code and body so the logging middleware can
// report what was actually sent to the client.
type recorder struct {
	http.ResponseWriter
	status int
	buf    *bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}
