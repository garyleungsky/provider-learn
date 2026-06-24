# Testing the provider in-cluster

The "real" deployment: the provider runs as a **Pod inside `kind`**, installed as
a Crossplane `Provider` package, managed by Crossplane core. This exercises the
packaging path (image + `.xpkg`), RBAC, and the runtime config — things
out-of-cluster `go run` never touches.

```
  kind cluster
  ┌──────────────────────────────────────────────┐      host process
  │ crossplane-system: crossplane core            │      ┌──────────────┐
  │   └─ Provider pod (our runtime image, --debug)│─HTTP▶│ mock-apiserver│
  │ default: Instance CR + ProviderConfig + Secret│      │  :8088        │
  └──────────────────────────────────────────────┘      └──────────────┘
                         host.docker.internal:8088 ───────────▶
```

**Key difference vs out-of-cluster:** the controller is *in* the cluster, so the
creds endpoint must be `http://host.docker.internal:8088` (Docker Desktop's
host alias), not `localhost`.

## Prerequisites

- `go`, `docker`, `kind`, `kubectl`, `helm`, and the `crossplane` CLI on PATH.
- Run everything from the repo root.

## 1. Build the image and package

```bash
make build
docker images | grep provider-learn          # runtime image: build-<hash>/provider-learn-<arch>
ls _output/xpkg/linux_*/                      # the .xpkg artifact
```

`make build` cross-compiles the linux binary, bakes the distroless **runtime
image** into the docker daemon, and packages CRDs + image into a **`.xpkg`**.

## 2. Create the cluster and install Crossplane

```bash
kind create cluster --name=provider-learn-dev
helm repo add crossplane-stable https://charts.crossplane.io/stable && helm repo update
helm install crossplane crossplane-stable/crossplane \
  --namespace crossplane-system --create-namespace --wait
```

## 3. Start the mock API server on the host

```bash
cd hack/mock-apiserver && go build -o /tmp/mock-apiserver . && cd -
/tmp/mock-apiserver -addr :8088 -token mock-secret-token   # leave running
```

## 4. Load the runtime image into kind

```bash
ARCH=$(go env GOARCH)
IMG=$(docker images --format '{{.Repository}}' | grep "provider-learn-$ARCH" | head -1)
kind load docker-image "$IMG" --name provider-learn-dev
echo "$IMG"
```

## 5. Inject the package into Crossplane's cache (registry-free)

Mirrors what `build/makelib/local.xpkg.mk` automates: add a throwaway sidecar
that shares the package-cache volume, then copy the extracted package in.

```bash
kubectl -n crossplane-system patch deploy crossplane --type='json' -p='[{"op":"add","path":"/spec/template/spec/containers/1","value":{"image":"alpine","name":"dev","command":["sleep","infinity"],"volumeMounts":[{"mountPath":"/tmp/cache","name":"package-cache"}]}},{"op":"add","path":"/spec/template/metadata/labels/patched","value":"true"}]'
kubectl -n crossplane-system wait deploy crossplane --for condition=Available --timeout=120s

mkdir -p _output/xpkg/cache
PKG=$(ls _output/xpkg/linux_*/*.xpkg | head -1)
crossplane xpkg extract --from-xpkg "$PKG" -o _output/xpkg/cache/provider-learn.gz

XPPOD=$(kubectl -n crossplane-system get pod -l app=crossplane,patched=true -o jsonpath='{.items[0].metadata.name}')
kubectl -n crossplane-system cp _output/xpkg/cache -c dev "$XPPOD":/tmp
```

## 6. Install the Provider from the local cache

A `DeploymentRuntimeConfig` points the Provider at the kind-loaded runtime image
and adds `--debug`; `packagePullPolicy: Never` makes Crossplane use the cache.

```bash
sed "s|__IMG__|$IMG|" <<'EOF' | kubectl apply -f -
apiVersion: pkg.crossplane.io/v1beta1
kind: DeploymentRuntimeConfig
metadata: { name: runtimeconfig-provider-learn }
spec:
  deploymentTemplate:
    spec:
      selector: {}
      template:
        spec:
          containers:
          - name: package-runtime
            image: __IMG__
            args: ["--debug"]
---
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata: { name: provider-learn }
spec:
  package: provider-learn.gz
  packagePullPolicy: Never
  skipDependencyResolution: true
  runtimeConfigRef: { name: runtimeconfig-provider-learn }
EOF

kubectl wait provider.pkg provider-learn --for=condition=Healthy --timeout=180s
kubectl -n crossplane-system get pods -l pkg.crossplane.io/provider=provider-learn
```

## 7. Apply ProviderConfig with the host-reachable endpoint

The Provider package installs the `ProviderConfig`/`Instance` CRDs. Apply the
bundle, then override the creds endpoint to `host.docker.internal`:

```bash
kubectl apply -f examples/provider/config.yaml
kubectl patch secret example-provider-secret -n default --type=merge \
  -p '{"stringData":{"credentials":"{\"endpoint\":\"http://host.docker.internal:8088\",\"token\":\"mock-secret-token\"}"}}'
```

## 8. Apply the Instance and observe

```bash
kubectl apply -f examples/database/instance.yaml
kubectl get instance.database.learn.garyleungsky.io -n default -o wide
kubectl -n crossplane-system logs -l pkg.crossplane.io/provider=provider-learn -f
```

Expect `READY=True / SYNCED=True`, the provider pod log showing the reconcile,
and the host mock log showing `GET 404 -> POST 201 -> GET 200`.

## 9. Tear down

```bash
kubectl delete -f examples/database/instance.yaml
kind delete cluster --name=provider-learn-dev      # stop the mock with Ctrl-C
```

## Notes & gotchas

- **`host.docker.internal`, not `localhost`.** From inside the cluster,
  `localhost` is the Pod itself. The host alias works on Docker Desktop; on plain
  Linux, use the host's bridge IP (often `172.17.0.1`) instead.
- **Iteration is slower than out-of-cluster.** After a code change you must
  rebuild, `kind load`, re-extract/copy, and re-create the Provider. Use the
  out-of-cluster loop for fast iteration; use this to validate packaging/runtime.
- **Make-helper shortcut.** The build submodule ships
  `build/makelib/controlplane.mk` and `build/makelib/local.xpkg.mk`. Adding
  `-include` lines for them in the `Makefile` enables `make controlplane.up` and
  `make local.xpkg.deploy.provider.provider-learn`, which automate steps 2 and
  4-6 above.
