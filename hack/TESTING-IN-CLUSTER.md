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

Mirrors what `crossplane/build`'s `local.xpkg.mk` automates: add a throwaway
sidecar that shares the package-cache volume, then copy the extracted package in.

> **Crossplane v2 cache naming.** v2 rejects a bare `provider-learn.gz` in
> `spec.package` — it must be a fully-qualified OCI reference. With
> `packagePullPolicy: Never` the cache key is the package's **FriendlyID**:
> `ToDNSLabel(trunc(source,50) + "-" + trunc(digest,12))`. Write the cache under
> both the `source@digest.gz` path *and* the FriendlyID name so any v2.x finds
> it. For local installs the digest is a fixed placeholder (`sha256:0…0`) and the
> source is `xpkg.crossplane.internal/dev/<pkgname>`, where `<pkgname>` is the
> `.xpkg` basename with its `-vX.Y.Z…` suffix stripped (→ `provider-learn`).

```bash
# dev sidecar sharing the package-cache volume (core mounts it at /cache/xpkg)
kubectl -n crossplane-system patch deploy crossplane --type='json' -p='[{"op":"add","path":"/spec/template/spec/containers/1","value":{"image":"alpine","name":"dev","command":["sleep","infinity"],"volumeMounts":[{"mountPath":"/tmp/cache","name":"package-cache"}]}},{"op":"add","path":"/spec/template/metadata/labels/patched","value":"true"}]'
kubectl -n crossplane-system wait deploy crossplane --for condition=Available --timeout=120s
kubectl -n crossplane-system wait pods -l app=crossplane,patched=true --for condition=Ready --timeout=120s

DIGEST=sha256:0000000000000000000000000000000000000000000000000000000000000000
PKG=$(ls -t _output/xpkg/linux_*/*.xpkg | head -1)
PKGNAME=$(basename "$PKG" | sed 's/-v\([0-9]*\.[0-9]*\.[0-9]*.*\)\.xpkg//')   # -> provider-learn
SOURCE="xpkg.crossplane.internal/dev/$PKGNAME"
FRIENDLY=$(printf '%.50s-%.12s' "$SOURCE" "$DIGEST" | sed 's/[^a-z0-9]/-/g' | cut -c1-63 | sed 's/-*$//')

rm -rf _output/xpkg/cache
mkdir -p "_output/xpkg/cache/xpkg.crossplane.internal/dev"
crossplane xpkg extract --from-xpkg "$PKG" -o "_output/xpkg/cache/$SOURCE@$DIGEST.gz"
cp "_output/xpkg/cache/$SOURCE@$DIGEST.gz" "_output/xpkg/cache/$FRIENDLY.gz"

# /tmp/cache is the volume mountpoint (can't rm the dir) — clear its contents only
XPPOD=$(kubectl -n crossplane-system get pod -l app=crossplane,patched=true -o jsonpath='{.items[0].metadata.name}')
kubectl -n crossplane-system exec "$XPPOD" -c dev -- sh -c 'rm -rf /tmp/cache/* /tmp/cache/xpkg.crossplane.internal'
kubectl -n crossplane-system cp _output/xpkg/cache -c dev "$XPPOD":/tmp

# verify from the sidecar — the core container is distroless (no shell/ls)
kubectl -n crossplane-system exec "$XPPOD" -c dev -- find /tmp/cache -type f
```

## 6. Install the Provider from the local cache

A `DeploymentRuntimeConfig` points the Provider at the kind-loaded runtime image
and adds `--debug`. `imagePullPolicy: IfNotPresent` is required because the image
was `kind load`-ed as `:latest` (which otherwise defaults to `Always` and fails
to pull). `packagePullPolicy: Never` makes Crossplane install from the cache, and
`package` is the `$SOURCE@$DIGEST` ref the cache was keyed under in step 5.

```bash
sed -e "s|__IMG__|$IMG|" -e "s|__PKG__|$SOURCE@$DIGEST|" <<'EOF' | kubectl apply -f -
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
            imagePullPolicy: IfNotPresent
            args: ["--debug"]
---
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata: { name: provider-learn }
spec:
  package: __PKG__
  packagePullPolicy: Never
  skipDependencyResolution: true
  runtimeConfigRef: { name: runtimeconfig-provider-learn }
EOF

kubectl wait provider.pkg provider-learn --for=condition=Healthy --timeout=180s
kubectl -n crossplane-system get pods -l pkg.crossplane.io/provider=provider-learn
```

## 7. Grant the provider RBAC to read CRDs (required for the gated start)

The provider uses crossplane-runtime's **gated setup**: a `crd-gate` controller
waits for the `Instance` CRD to be *established* before starting the managed
reconciler. Crossplane's rbac-manager grants the provider its own CRs and core
resources, but **not** `customresourcedefinitions` read access — so the gate
stalls and the Instance controller never starts (the log repeats
`cannot list ... customresourcedefinitions ... forbidden`). Grant it, then
restart the provider pod so the gate re-evaluates:

```bash
SA=$(kubectl -n crossplane-system get sa -o name | grep provider-learn | head -1 | cut -d/ -f2)
cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: { name: crossplane-provider-learn-crd-reader }
rules:
- apiGroups: ["apiextensions.k8s.io"]
  resources: ["customresourcedefinitions"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: { name: crossplane-provider-learn-crd-reader }
roleRef: { apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: crossplane-provider-learn-crd-reader }
subjects:
- { kind: ServiceAccount, name: $SA, namespace: crossplane-system }
EOF

kubectl -n crossplane-system delete pod -l pkg.crossplane.io/provider=provider-learn
kubectl -n crossplane-system wait pod -l pkg.crossplane.io/provider=provider-learn --for=condition=Ready --timeout=120s
```

After the restart the log shows `gvk is ready` for the Instance kind, then
`Starting Controller … "controllerKind":"Instance"`.

## 8. Apply ProviderConfig with the host-reachable endpoint

The Provider package installs the `ProviderConfig`/`Instance` CRDs. Apply the
creds bundle, swapping the endpoint to `host.docker.internal` so the in-cluster
controller can reach the host mock:

```bash
sed 's|http://localhost:8088|http://host.docker.internal:8088|' \
  examples/provider/config.yaml | kubectl apply -f -
```

## 9. Apply the Instance and observe

```bash
kubectl apply -f examples/database/instance.yaml
kubectl get instance.database.learn.garyleungsky.io -n default -o wide
kubectl -n crossplane-system logs -l pkg.crossplane.io/provider=provider-learn -f
```

Expect `READY=True / SYNCED=True`, the provider pod log showing the reconcile,
and the host mock log showing `GET 404 -> POST 201 -> GET 200`.

## 10. Tear down

```bash
kubectl delete -f examples/database/instance.yaml
kind delete cluster --name=provider-learn-dev      # stop the mock with Ctrl-C
```

## Notes & gotchas

- **`host.docker.internal`, not `localhost`.** From inside the cluster,
  `localhost` is the Pod itself. The host alias works on Docker Desktop; on plain
  Linux, use the host's bridge IP (often `172.17.0.1`) instead.
- **The gated start needs CRD-read RBAC (step 7).** `SetupGated` will silently
  never start the Instance controller without it — the Provider still reports
  `Healthy` (that only covers package install), so watch the *pod log*, not just
  the Provider condition, when reconciles don't happen.
- **Iteration is slower than out-of-cluster.** After a code change you must
  rebuild, `kind load`, re-extract/copy, and re-create the Provider. Use the
  out-of-cluster loop for fast iteration; use this to validate packaging/runtime.
- **Make-helper shortcut (needs an up-to-date submodule).** The build submodule
  ships `build/makelib/controlplane.mk` and `build/makelib/local.xpkg.mk`; adding
  `-include` lines for them in the `Makefile` exposes `make controlplane.up` and
  `make local.xpkg.deploy.provider.provider-learn`, which automate steps 2 and
  4-7 above. **Caveat:** the currently vendored `local.xpkg.mk` predates
  Crossplane v2 and still emits the legacy `spec.package: <name>-<version>.gz`,
  which v2.x rejects (see step 5). Run `make submodules` to pull the current
  `crossplane/build` recipe (digest ref + dual cache keys) before relying on it.
