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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
)

// defaultEndpoint is the address of a locally running mock-apiserver. It lets
// the provider work out of the box for development when no endpoint is supplied
// via the ProviderConfig credentials.
const defaultEndpoint = "http://localhost:8088"

// apiInstance is the wire representation of an instance in the external API. It
// mirrors the mock-apiserver's instance type: ConfigurableField is client-set,
// ObservableField is server-computed.
type apiInstance struct {
	Name              string `json:"name"`
	ConfigurableField string `json:"configurableField"`
	ObservableField   string `json:"observableField,omitempty"`
}

// providerCredentials is the JSON shape expected in the ProviderConfig
// credentials. It carries the base URL of the external API.
type providerCredentials struct {
	Endpoint string `json:"endpoint"`
}

// apiClient talks to the external instance API over HTTP.
type apiClient struct {
	baseURL string
	http    *http.Client
}

// newAPIClient builds an apiClient from ProviderConfig credential bytes, which
// are expected to be JSON of the form {"endpoint":"http://..."}. Empty or
// missing credentials fall back to defaultEndpoint.
func newAPIClient(creds []byte) (*apiClient, error) {
	endpoint := defaultEndpoint
	if len(creds) > 0 {
		var c providerCredentials
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, errors.Wrap(err, "cannot parse provider credentials")
		}
		if c.Endpoint != "" {
			endpoint = c.Endpoint
		}
	}
	return &apiClient{
		baseURL: strings.TrimRight(endpoint, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// do issues a request, JSON-encoding body when non-nil.
func (c *apiClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// Get fetches one instance. found is false (with a nil error) on HTTP 404,
// which is how Observe learns the external resource does not exist yet.
func (c *apiClient) Get(ctx context.Context, name string) (in *apiInstance, found bool, err error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/instances/"+name, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close() //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		out := &apiInstance{}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return nil, false, err
		}
		return out, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, unexpected(resp)
	}
}

// Create provisions a new instance; the server assigns ObservableField.
func (c *apiClient) Create(ctx context.Context, in apiInstance) (*apiInstance, error) {
	resp, err := c.do(ctx, http.MethodPost, "/v1/instances", in)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated {
		return nil, unexpected(resp)
	}
	out := &apiInstance{}
	return out, json.NewDecoder(resp.Body).Decode(out)
}

// Update changes the mutable ConfigurableField of an existing instance. Name is
// sent in the body too (as well as the path) so the request is correct against
// APIs that identify the resource from the payload rather than the URL.
func (c *apiClient) Update(ctx context.Context, name, configurableField string) (*apiInstance, error) {
	resp, err := c.do(ctx, http.MethodPut, "/v1/instances/"+name, apiInstance{Name: name, ConfigurableField: configurableField})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, unexpected(resp)
	}
	out := &apiInstance{}
	return out, json.NewDecoder(resp.Body).Decode(out)
}

// Delete removes an instance. A 404 is treated as success (already gone).
func (c *apiClient) Delete(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/instances/"+name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return unexpected(resp)
	}
	return nil
}

// unexpected wraps an out-of-contract HTTP response as an error.
func unexpected(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return errors.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
