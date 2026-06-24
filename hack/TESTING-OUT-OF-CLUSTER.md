# Testing the provider out-of-cluster

The fastest dev loop. The controller runs as a **plain process on your host**
(`go run`) against a `kind` cluster, talking to the `mock-apiserver` on
`localhost`. No image build, no packaging.

```
  host process                kind cluster              host process
  ┌────────────────┐  watch   ┌───────────────┐         ┌──────────────┐
  │ provider        │◀────────│ Instance CR    │         │ mock-apiserver│
  │ (go run)        │         │ ProviderConfig │         │  :8088        │
  │                 │── HTTP ─┼────────────────┼────────▶│  Bearer auth  │
  └────────────────┘ localhost└───────────────┘         └──────────────┘
```

Because the controller runs on the host, the creds endpoint is
`http://localhost:8088` and reaches the mock directly.

## Prerequisites

- `go`, `docker`, `kind`, `kubectl` on PATH.
- Repo root is the working directory for every command below.

## 1. Build and start the mock API server

```bash
cd hack/mock-apiserver && go build -o /tmp/mock-apiserver . && cd -
/tmp/mock-apiserver -addr :8088 -token mock-secret-token
```

Leave it running in its own terminal — it logs every request/response. The
token is required on all `/v1/...` calls (`/healthz` is exempt).

## 2. Create a kind cluster

```bash
kind create cluster --name=provider-learn-dev
kubectl config current-context        # -> kind-provider-learn-dev
```

## 3. Install the CRDs

```bash
kubectl apply -R -f package/crds
```

Installs the `Instance`, `ProviderConfig`, and usage CRDs.

## 4. Apply the ProviderConfig + creds Secret

```bash
kubectl apply -f examples/provider/config.yaml
```

The Secret carries JSON creds via `stringData`:
`{"endpoint":"http://localhost:8088","token":"mock-secret-token"}`.
Verify it decodes as expected:

```bash
kubectl get secret example-provider-secret -n default \
  -o jsonpath='{.data.credentials}' | base64 -d ; echo
```

## 5. Run the controller on the host

```bash
go run cmd/provider/main.go --debug
# or, equivalently, the convenience target:
make run
```

Wait for `Starting workers ... "controller": "managed/instance..."`.

## 6. Apply the Instance and watch it reconcile

```bash
kubectl apply -f examples/database/instance.yaml
sleep 5
kubectl get instance.database.learn.garyleungsky.io -n default -o wide
kubectl get instance.database.learn.garyleungsky.io example-instance \
  -n default -o jsonpath='{.status.atProvider}{"\n"}'
```

Expect `READY=True / SYNCED=True` and the mock server log to show
`GET 404 -> POST 201 -> GET 200`. `atProvider` carries the server-computed
`observableField` (`example-instance.instances.mock.local`).

## 7. (Optional) Exercise update and delete

```bash
# Update: change the mutable field, re-apply, watch a PUT in the mock log.
kubectl patch instance.database.learn.garyleungsky.io example-instance \
  -n default --type=merge -p '{"spec":{"forProvider":{"configurableField":"large"}}}'

# Delete: watch DELETE 204, then GET 404.
kubectl delete -f examples/database/instance.yaml
```

## 8. Tear down

```bash
# Stop the controller (Ctrl-C) and the mock server (Ctrl-C), then:
kind delete cluster --name=provider-learn-dev
# or the convenience target:
make dev-clean
```

## Notes & gotchas

- **Endpoint is `localhost`** only because the controller runs on the host. When
  the provider runs *inside* the cluster, use the in-cluster guide instead — the
  endpoint changes to `host.docker.internal`.
- **Token must be in the Secret.** Omitting it makes the client fall back to the
  built-in `defaultToken`, which only works if the mock was started with that
  same default.
- **`make dev`** does steps 2, 3, and 5 in one shot (cluster + CRDs + controller),
  but running the parts by hand lets you read each log stream while you
  apply/patch/delete — better for understanding what each call does.
- The mock server's request logger prints method/path/body but **never headers**,
  so the bearer token is not logged.
