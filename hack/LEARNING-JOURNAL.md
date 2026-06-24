# Crossplane Provider Development — Learning Journal

Personal notes for building a Crossplane provider from `crossplane/provider-template`.
This file lives under `hack/` **on purpose**: `make provider.prepare` excludes
`hack/**` from its `template` -> `learn` rewrite, so these notes stay accurate
while remaining version-controlled (see "Gotcha" below).

- Working repo: `provider-learn` (`garyleungsky/provider-learn`)
- Template: https://github.com/crossplane/provider-template

## Decisions

| Item              | Value                          | Notes                                         |
| ----------------- | ------------------------------ | --------------------------------------------- |
| Provider name     | `Learn`                        | CamelCase; lowercases to `learn`              |
| Base API domain   | `learn.garyleungsky.io`        | template default would be `learn.crossplane.io` |
| Group             | `database`                     | single short label -> `database.learn.garyleungsky.io` |
| Kind              | `Instance`                     | CamelCase, singular managed resource          |
| Go module / org   | `github.com/garyleungsky/provider-learn` | template default keeps `crossplane` org |

## Key concepts learned

- **API group vs Go module path** are different namespaces:
  - Go module path = where the code lives (`github.com/<org>/provider-learn`).
  - API group = how Kubernetes names the CRD (`<group>.<provider>.<domain>`,
    e.g. `sample.learn.garyleungsky.io`).
- The `group` arg to `provider.addtype` is a **single segment** (no dots). The
  base domain is combined automatically.
- The base domain is just a namespacing string — it does NOT need to be a domain
  you own / it is never resolved via DNS.

## Testing architecture (provider <-> Go API server)

Goal: watch the HTTP request/response between the provider and a Go-written API
server during testing.

A provider has TWO connections:

```
  Kubernetes API  <-- watches/updates CRs --  Provider (controller)  -- HTTP -->  Go API server
  (stores CRDs/MRs)                            Observe/Create/Update/Delete        (external API)
```

Key point: in local dev the provider runs OUT-OF-CLUSTER (on the host), so it can
reach the API server at `http://localhost:PORT` directly.

| Test level                       | kind cluster? | API server location        |
| -------------------------------- | ------------- | -------------------------- |
| Unit (instance_test.go)          | No            | `httptest.Server` (in-proc) |
| Local run (`make run`/`make dev`)| Yes (for CRs) | host `localhost:PORT`       |
| Full in-cluster E2E              | Yes           | in-cluster Service / host.docker.internal |

Decisions:
- API server does NOT need to live in the kind cluster (provider runs on host).
- Best req/resp visibility: unit tests with `httptest`; live loop with `make run`.
- Build our OWN Go API server (in-memory CRUD) for testing.
- Location: `hack/mock-apiserver/` (own go.mod, stdlib net/http). hack/** is
  excluded from `make provider.prepare`, so it is safe even if prepare re-runs.
- Ordering: scaffold the provider (incl. `prepare`) FIRST, then build the server.
  `prepare` is a ONCE-ONLY step; never run it after adding custom code.

Recommended live loop:

```bash
# terminal 1: run the Go API server (logs each request/response), e.g. :8080
# terminal 2:
make run
# terminal 3:
kubectl apply -f examples/.../instance.yaml
kubectl get instance.database.learn.garyleungsky.io -w
```

The HTTP calls live in the Instance controller's ExternalClient
(`internal/controller/instance/instance.go`): Observe/Create/Update/Delete will
call the Go API server's endpoints. The base URL/creds come from ProviderConfig.

## Prerequisites (already installed on this machine)

```
go 1.26.4 | docker 29.5.3 | kubectl v1.36.2 | kind 0.29.0 | make 3.81 | git | gh
```

## Steps completed

### 1. Import template into the empty repo (kept my own `origin`)

```bash
git remote add template https://github.com/crossplane/provider-template.git
git fetch template --depth=1
git reset --hard template/main        # main now points at template content
```

### 2. Squash to a single clean baseline commit (option B)

Avoids inheriting the template's ~250 commits; mirrors GitHub's
"Use this template" behavior.

```bash
git checkout --orphan newbase
git add -A
git commit -m "chore: import crossplane provider-template baseline" \
  -m "Initialize provider-learn from crossplane/provider-template ..." \
  -m "Source: https://github.com/crossplane/provider-template"
git branch -M main                    # replace main with the squashed branch
```

Verified content is identical to the template via tree hash:

```bash
git rev-parse main^{tree}             # == template/main^{tree}
# both = 5a1728ecdd4899d786a13b0ef622a922aa6d403b
git diff --stat main template/main    # empty = identical
```

### 3. Push baseline to my repo

```bash
git push -u origin main
```

### 3b. Add this learning journal (kept in hack/ so prepare won't rewrite it)

```bash
# created at repo root, then moved into the prepare-excluded hack/ dir
mv LEARNING-JOURNAL.md hack/LEARNING-JOURNAL.md
git add hack/LEARNING-JOURNAL.md
git commit -m "docs: add learning journal for provider development"
```

### 4. Initialize the build submodule

Required before any `make` target works (the Makefile does
`-include build/makelib/*.mk`).

```bash
make submodules            # clones crossplane/build -> ./build (pinned b964dbe)
```

What `make submodules` actually runs:

```makefile
submodules:
	git submodule sync                          # copy URL from .gitmodules -> .git/config
	git submodule update --init --recursive     # clone + checkout the pinned commit
```

What a git submodule is:

- `build/` is a pointer to a specific commit of another repo (`crossplane/build`).
- Our repo stores ONLY a "gitlink", not the files:
  `160000 b964dbe... build`  (mode 160000 = submodule; b964dbe = pinned commit).
- Declared in `.gitmodules`:
  `path = build`, `url = https://github.com/crossplane/build`.
- Before running, `build/` was an EMPTY dir (pointer present, files absent).

What the commands did:

- `sync`  -> ensures git knows where to fetch from.
- `update --init --recursive` -> clones the repo into `build/` and checks out the
  EXACT pinned commit `b964dbe` (not latest main -> reproducible builds).
- Clean status shows a leading space: ` b964dbe... build` (checked out, matches pin).

Why it matters: `build/makelib/` provides the shared Make libraries the Makefile
`-include`s, which define the real targets:

```
golang.mk     -> make build / test / lint / reviewable
k8s_tools.mk  -> downloads kubectl/kind/helm into a tool cache
xpkg.mk + imagelight.mk -> build the OCI image and Crossplane package (.xpkg)
```

Without the submodule populated, those includes are empty and targets like
`make build` / `make reviewable` would not exist.

### 5. Rename the provider (ONCE-ONLY)

```bash
make provider.prepare provider=Learn
```

Observed results (verified with `git status` / `git grep`):

- Removed example: `apis/sample/**`, `internal/controller/mytype/**`.
- Renamed: `apis/template.go -> apis/learn.go`,
  `internal/controller/register.go -> internal/controller/learn.go`,
  `cluster/images/provider-template -> cluster/images/provider-learn`.
- Replaced `template -> learn` / `Template -> Learn` in tracked files
  (build/, hack/, go.* excluded). go.mod module became
  `github.com/crossplane/provider-learn`.
- ProviderConfig API group is now `learn.crossplane.io` (changed to
  `learn.garyleungsky.io` in a later step).
- Generated CRD filenames still say `template`/`sample` (e.g.
  `package/crds/template.crossplane.io_*.yaml`) -> regenerated by
  `make generate`/`reviewable`, so harmless.

Leftover that prepare MISSED (fix during the "module org" step):

```
.golangci.yml:114:   - github.com/crossplane/provider-template
# -> should be github.com/garyleungsky/provider-learn (depguard allow-rule)
```

Tip: because the tree was clean before, `git diff HEAD` shows exactly what
prepare changed.

## Next steps (planned)

```bash
# 4. Initialize the shared CI/CD build submodule (crossplane/build -> ./build)
make submodules

# 5. Rename the provider template -> learn / Template -> Learn (RUN ONCE)
make provider.prepare provider=Learn

# 6. Scaffold the managed resource + controller (replaces the removed example)
make provider.addtype provider=Learn group=database kind=Instance

# 7. Set custom domain: replace learn.crossplane.io -> learn.garyleungsky.io
#    (safe: crossplane-runtime's own crossplane.io refs lack the "learn." prefix)

# 8. Fix module org: github.com/crossplane/provider-learn
#    -> github.com/garyleungsky/provider-learn  (go.mod, Makefile, imports)

# 9. Wire up registration:
#    - apis/learn.go            (register the database group)
#    - internal/controller/learn.go (register Instance controller)
#    - internal/controller/register.go (SetupGated)

# 10. Generate code, lint, test
make reviewable

# 11. Build
make build

# 12. Run locally against a kind cluster, then apply the example
make dev        # creates kind cluster, applies CRDs, runs controller
# or: make run  # run controller out-of-cluster against current kubeconfig
```

## How `make provider.prepare` works (hack/helpers/prepare.sh)

- `git rm -r apis/sample` and `internal/controller/mytype` (removes example).
- `git grep -l 'template'` then `sed s/template/learn/g` across tracked files,
  excluding `build/**`, `go.*`, `hack/**`. Same for `Template` -> `Learn`.
- Renames `apis/template.go`, `internal/controller/register.go`,
  `cluster/images/provider-template`.
- **Run once only.** Re-running needs a clean git state (stash/reset).

## Gotcha: why this journal lives in hack/

`prepare.sh` uses `git grep` + `sed` to replace "template" -> "learn" across
tracked files, but it **excludes** `build/**`, `go.*`, and `hack/**`
(`REPLACE_FILES='./* ./.github :!build/** :!go.* :!hack/**'`). Putting the journal
at the repo root would get its references (e.g. `provider-template`) rewritten to
`provider-learn`, corrupting the historical notes. Keeping it in `hack/` means it
stays both **tracked/committed** and **untouched** by `prepare`.

## Step 6 done: `make provider.addtype provider=Learn group=database kind=Instance`

Scaffolds a new managed resource + its controller (replacing the example removed
by `prepare`). It first installed `gomplate` (template renderer) into the tool
cache, then generated:

```
apis/database/database.go                     # group doc package
apis/database/v1alpha1/doc.go                 # +groupName marker for codegen
apis/database/v1alpha1/groupversion_info.go   # Group/Version + SchemeBuilder
apis/database/v1alpha1/instance_types.go      # Instance CRD Go structs
internal/controller/instance/instance.go      # the ExternalClient skeleton
internal/controller/instance/instance_test.go # table-driven test stub
```

What the generated `instance_types.go` gives us:
- `InstanceParameters` (forProvider, desired) with one `configurableField`.
- `InstanceObservation` (atProvider, observed) with `configurableField` +
  `observableField`.
- `InstanceSpec` embeds `xpv2.ManagedResourceSpec` (namespaced MR, v2 runtime);
  `InstanceStatus` embeds `xpv1.ResourceStatus`.
- `+kubebuilder` markers => Namespaced scope, status subresource, print columns.
- `init()` registers `Instance`/`InstanceList` with the scheme.

What `instance.go` gives us (the bits we'll replace):
- `connector.Connect` already resolves `ProviderConfig`/`ClusterProviderConfig`
  and extracts credentials — keep this.
- `newNoOpService` + `external{service interface{}}` with `Observe/Create/Update/
  Delete` that just `fmt.Printf` and return "exists & up-to-date". This is the
  HTTP-client logic we implement against the mock API server later.

Important: `addtype` generates files but does **not** wire them in. Still pending:
- `apis/learn.go`            — add the `database` group to `AddToSchemes`.
- `internal/controller/learn.go` — call `instance.SetupGated` in `SetupGated`.

## Step 7 done: custom domain `learn.crossplane.io` -> `learn.garyleungsky.io`

Changed only the **source-of-truth** API group declarations (hand-edited):

```
apis/v1alpha1/doc.go                       // +groupName marker (core group)
apis/v1alpha1/register.go                  // Group const (core group)
apis/database/v1alpha1/groupversion_info.go// +groupName marker + Group const
examples/provider/config.yaml              // ProviderConfig/ClusterProviderConfig apiVersion
```

Deliberately left untouched:
- `package/crds/*.yaml` — these are **generated**; `make generate`/`make
  reviewable` will regenerate them from the markers above (do not hand-edit).
- `examples/sample/mytype.yaml` — stale leftover from the template example,
  removed in a later cleanup.
- SPDX/`zz_generated` `crossplane.io` refs — those are the Crossplane Authors
  copyright URL, not our API group.

Why this is safe: crossplane-runtime's own `crossplane.io` references never carry
the `learn.` prefix, so targeting `learn.crossplane.io` only touches our groups.
