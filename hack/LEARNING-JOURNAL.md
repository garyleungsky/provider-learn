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

## Step 8 done: fix Go module org -> `github.com/garyleungsky/provider-learn`

`prepare` only changed `provider-template` -> `provider-learn`; it kept the
`crossplane` org in the module path. Replaced
`github.com/crossplane/provider-learn` -> `github.com/garyleungsky/provider-learn`
everywhere except `hack/` (journal keeps the old path as a historical note):

```
go.mod  apis/learn.go  cmd/provider/main.go  internal/controller/learn.go
internal/controller/config/config.go  internal/controller/instance/*.go
package/crossplane.yaml  PROVIDER_CHECKLIST.md
```

zsh gotcha: `for f in $files` does **not** word-split an unquoted variable in zsh
(unlike bash). Used `git grep -l ... | while IFS= read -r f` instead.

`go build ./...` now still fails — but for an **unrelated, pre-existing** reason:
the registration files reference the example packages `prepare` deleted:

```
apis/learn.go            -> apis/sample/v1alpha1        (deleted)
internal/controller/learn.go -> internal/controller/mytype (deleted)
```

Fixed in the next step (wire up the `database`/`Instance` registration).

## Step 9 done: wire up registration (replace example refs)

`prepare` renamed `internal/controller/register.go` -> `internal/controller/learn.go`
(so there is **no** `register.go` to edit), but neither `prepare` nor `addtype`
re-pointed the registration files away from the deleted example packages. Two
hand-edits:

```
apis/learn.go:
  - samplev1alpha1 (apis/sample/v1alpha1)  ->  databasev1alpha1 (apis/database/v1alpha1)
  - AddToSchemes: register databasev1alpha1.SchemeBuilder.AddToScheme

internal/controller/learn.go:
  - import mytype  ->  import instance (internal/controller/instance)
  - setup list:   mytype.SetupGated  ->  instance.SetupGated
```

These resolve the deleted-package build errors. `go build ./...` now fails only
on:

```
*Instance / *InstanceList does not implement runtime.Object
  (missing method DeepCopyObject)
```

That method is **generated** by `controller-gen` into `zz_generated.deepcopy.go`
from the `+kubebuilder:object` markers. We produce it next via `make generate`
(part of `make reviewable`). So the wiring itself is complete; the build goes
green after codegen.

## Step 10: generate, lint, test

### 10a. The Makefile module-org miss

First `make generate` printed warnings and changed nothing:

```
go: warning: "github.com/crossplane/provider-learn/cmd/..." matched no packages
```

Cause: the module-org sed in Step 8 only matched the **literal**
`github.com/crossplane/provider-learn`, but the Makefile builds the path from a
variable:

```make
PROJECT_REPO := github.com/crossplane/$(PROJECT_NAME)   # PROJECT_NAME=provider-learn
```

So `GO_PROJECT` still pointed at the `crossplane` org, the `go generate` package
patterns matched nothing, and codegen silently no-op'd. Fixed line 4 ->
`github.com/garyleungsky/$(PROJECT_NAME)`. Lesson: when renaming, also grep for
the **composed** form, not just the full literal.

### 10b. `make generate` (controller-gen + angryjet)

After the fix, generate:
- removed the stale template/sample CRDs (`apis/generate.go` runs
  `rm -rf ../package/crds` first, then regenerates);
- wrote new CRDs at the correct domain, incl.
  `package/crds/database.learn.garyleungsky.io_instances.yaml`;
- generated `zz_generated.deepcopy.go` (DeepCopyObject), plus
  `zz_generated.managed.go` / `zz_generated.managedlist.go` (angryjet adds the
  `resource.Managed` accessor methods). `go build ./...` is now green.

Also removed the dead `examples/sample/mytype.yaml` (referenced the deleted
`MyType` and old domain).

### 10c. golangci-lint vs go1.26

`make lint` initially **panicked**:

```
panic: file requires newer Go version go1.26 (application built with go1.24)
```

The pinned `golangci-lint 2.1.2` prebuilt binary embeds go1.24's type-checker and
can't parse the go1.26 std library (local Go is 1.26.4). golangci-lint added
go1.26 support in **v2.9.0** (Feb 2026). Bumped the Makefile pin
`GOLANGCILINT_VERSION = 2.1.2 -> 2.12.2` (latest). Lint then ran and reported one
real issue — a gofmt `=`-alignment in the scaffolded `instance.go` const block;
`gofmt -w` fixed it. `make lint` => `0 issues`.

`go test ./...` passes (the scaffolded `instance_test.go` is the only test).

### 10d. Why the `zz_generated.*` files exist

Kubernetes and Crossplane are built around Go **interfaces**; our `Instance`
struct must implement them to be usable. Rather than hand-write ~200 lines of
mechanical boilerplate per type (and keep it in sync with every field), the
tooling generates it from `instance_types.go` + its marker comments. The `zz_`
prefix sorts these files last and each carries `// Code generated ... DO NOT EDIT.`

| File                          | Interface implemented | Generated by   | Required because                                   |
| ----------------------------- | --------------------- | -------------- | -------------------------------------------------- |
| `zz_generated.deepcopy.go`    | `runtime.Object`      | controller-gen | every K8s API type must be deep-copyable           |
| `zz_generated.managed.go`     | `resource.Managed`    | angryjet       | Crossplane's generic reconciler operates on it     |
| `zz_generated.managedlist.go` | `resource.ManagedList`| angryjet       | lets the reconciler/metrics iterate a list         |

**deepcopy -> `runtime.Object`.** Key method `DeepCopyObject() runtime.Object`.
The client cache (informers) hands controllers *shared pointers*; if code mutated
one in place it would corrupt the cache for every consumer, so the machinery
deep-copies before handing objects out or writing them back. This was the exact
build error before codegen: `*Instance does not implement runtime.Object (missing
method DeepCopyObject)`, because `SchemeBuilder.Register(&Instance{}, ...)`
requires it. Driven by the `// +kubebuilder:object:generate=true` / `:root=true`
markers.

**managed -> `resource.Managed`.** Crossplane's reconciler is generic — it works
on the `Managed` interface, not the concrete `Instance`. It needs uniform
getters/setters: status `Conditions` (Ready/Synced), `ProviderConfigReference`
(used in `Connect`), `ManagementPolicies`, connection-secret ref. Our
`managed.NewReconciler(..., resource.ManagedKind(...))` and
`WithTypedExternalConnector[*v1alpha1.Instance]` only compile because `Instance`
is a `resource.Managed`.

**managedlist -> `resource.ManagedList`.** Just `GetItems() []resource.Managed`
so list-wide ops work generically — e.g. the MR state-metrics recorder in
`instance.go` uses `&v1alpha1.InstanceList{}`.

Two practical notes:
- **Source of truth is `instance_types.go`.** These files are mechanical
  projections of the struct; change a field -> re-run `make generate`. Never edit
  by hand.
- **They are committed to git on purpose.** Even though generated, checking them
  in means anyone running `go build` (or importing the module) needs no codegen
  toolchain — standard Go practice.

## Step 11 done: `make build`

Produced three artifacts (all under `_output/`, which is gitignored — a build
never dirties the tree):

```
binary   _output/bin/linux_arm64/provider                       42M  static ELF, ARM64, stripped
image    build-<hash>/provider-learn-arm64:latest                45.8MB
package  _output/xpkg/linux_arm64/provider-learn-v0.0.0-14.gd282cb0.xpkg  15M
```

What happened, in order:
1. **Cross-compiled** a `linux/arm64` provider binary (`cmd/provider`) — static,
   stripped. `make build` builds for the target platform(s) in `PLATFORMS`, not
   the host; for local dev we use `make run` (host binary) later.
2. **Docker image**: `ADD bin/linux_arm64/provider` into a `distroless/static`
   base at `/usr/local/bin/crossplane-learn-provider`. Distroless = no shell, no
   package manager, just libc + the binary => tiny, minimal attack surface. This
   is the image a provider ships as.
3. **xpkg**: packaged the image + the CRDs from `package/crds` + `package/
   crossplane.yaml` into a Crossplane package (`.xpkg`, an OCI artifact). This is
   the unit you'd `crossplane xpkg push` / install into a control plane.

Version string `v0.0.0-14.gd282cb0` is `git describe`-style: `0.0.0` (no tags
yet) + `14` commits + short SHA `d282cb0`, so every package is traceable to an
exact commit.

`make build` vs `make run`:
- `make build` = artifacts for shipping (target-platform binary, image, xpkg).
- `make run`   = compile a **host** binary and run the controller out-of-cluster
  against the current kubeconfig (the fast dev loop we'll use shortly).

## Step 12: a mock external API to manage

Before writing the controller, we built the *thing the controller talks to* — a
tiny REST API standing in for a real cloud service. Having it first means that
when we implement Observe/Create/Update/Delete we have something concrete to call
and can watch the exact HTTP traffic each reconcile generates.

Location and layout (`hack/mock-apiserver/`):
```
main.go      server setup, instance/store types, JSON helpers, logging middleware
handlers.go  /v1/instances (GET list, POST create) and /v1/instances/{name}
             (GET, PUT, DELETE)
go.mod       github.com/garyleungsky/provider-learn/hack/mock-apiserver
```

Why its **own** `go.mod` (a second module inside the repo): it keeps a throwaway
dev tool's dependencies out of the provider's `go.mod`/`go.sum`. The provider
must stay lean — only the deps it actually ships with. A nested module is its own
universe; `go build ./...`, `make lint`, and `make reviewable` at the repo root
never see it. (This one happens to be stdlib-only, so it has zero deps anyway,
but the isolation principle holds the moment a tool needs a third-party import.)

The API deliberately **mirrors the Instance CRD** so the controller mapping is
one-to-one:
- `configurableField` — client-settable  -> maps to `spec.forProvider`
- `observableField`   — server-computed   -> maps to `status.atProvider`

On POST the server "provisions" the resource and assigns `observableField`
(`<name>.instances.mock.local`); PUT only lets you change `configurableField`.
That is exactly how a real API behaves: you declare desired config, the system
fills in computed/read-only attributes you observe later.

REST contract (what the controller will rely on):
```
GET    /v1/instances           200  list all
POST   /v1/instances           201  create (409 if name exists, 400 if no name)
GET    /v1/instances/{name}    200  read   (404 if absent)  <- drives Observe
PUT    /v1/instances/{name}    200  update configurableField (404 if absent)
DELETE /v1/instances/{name}    204  delete (404 if absent)
```
The `404 on GET` is the important one: it's how Observe will decide "the external
resource does not exist yet" and return `ResourceExists: false` so the reconciler
calls Create.

Logging middleware prints every exchange to stdout:
```
--> METHOD /path {request-body}
<-- STATUS Text (latency) {response-body}
```
Verified with a full curl lifecycle (empty list -> 404 -> create -> read ->
409 dup -> update -> read -> 204 delete -> 404), and the log showed each
request/response pair as expected.

Run it locally with:
```
cd hack/mock-apiserver && go run . -addr :8088
```

## Step 13: implement the ExternalClient (the heart of the provider)

This is where the provider stops being scaffolding and starts *doing* something.
A Crossplane managed-resource controller is generic: the crossplane-runtime
reconciler owns the control loop (watch, finalizers, conditions, requeue). Our
job is to fill in one small interface, `ExternalClient`, that maps the desired
state of an `Instance` onto the external system. We implemented it against the
mock API from step 12.

Two new/changed files in `internal/controller/instance/`:
```
client.go    NEW  — apiClient: HTTP wrapper over /v1/instances
instance.go  EDIT — Observe/Create/Update/Delete call the client
```

**The reconcile contract** (what each method must tell the reconciler):
```
Observe → does the external resource exist? is it up to date?
            ResourceExists:false   -> runtime calls Create
            ResourceExists:true +
              ResourceUpToDate:false -> runtime calls Update
            ResourceUpToDate:true    -> nothing to do
Create  → make it exist
Update  → make it match spec.forProvider
Delete  → make it gone (called when the MR is deleted)
```
The runtime, not us, decides *which* method to call; we only report truthfully in
Observe and act in the others. This is the key mental model of Crossplane.

**Identity: the external-name annotation.** `meta.GetExternalName(cr)` is the
resource's identity in the external API. By default the runtime sets it to the
MR's `metadata.name` before Observe runs, so it's always populated. Every method
keys off this name.

**Observe in detail:**
- `GET /v1/instances/{name}`.
- A **404 -> `ResourceExists:false`** — this single mapping is what makes the
  whole loop work; it's how the runtime learns it must Create.
- 200 -> copy `configurableField`/`observableField` into `status.atProvider`, set
  the `Available` condition, and compute `ResourceUpToDate` by comparing the
  observed `configurableField` to `spec.forProvider.configurableField`. If they
  differ, the runtime calls Update.
- `observableField` (server-computed) is also returned as a **connection detail**
  — the provider's way of publishing useful outputs to a secret.

**Create/Update/Delete:** POST / PUT / DELETE respectively. Create and Delete set
the `Creating` / `Deleting` conditions so users see lifecycle status via
`kubectl`. Update only sends `configurableField` (the one mutable field).

**Where the endpoint comes from.** The template already plumbs ProviderConfig ->
credentials -> `[]byte` into a `newServiceFn`. We replaced the no-op
`NoOpService` with `newAPIClient`, which parses the creds as
`{"endpoint":"http://..."}` and falls back to `http://localhost:8088` when empty
— so it works against the local mock with zero config, but a real endpoint can be
supplied via a ProviderConfig secret later.

**Testing without a real backend (`instance_test.go`).** Crossplane's own style is
table-driven tests with no external deps. Here we stand up an
`httptest.Server` that re-implements the mock's `/v1/instances` contract
in-memory, point an `apiClient` at `srv.URL`, and drive the **full lifecycle**
through the real HTTP client:
```
Observe(absent)    -> ResourceExists:false
Create             -> observableField published
Observe(present)   -> exists + up to date, status populated
spec change        -> Observe reports ResourceUpToDate:false (drift)
Update             -> Observe reports up to date again
Delete             -> Observe reports gone
```
Plus a guard test that Observe makes no HTTP call when external-name is empty.
The mock-apiserver lives in a *separate Go module*, so it can't be imported into
the provider's tests — re-creating the tiny contract in the test is the clean
way to keep the test self-contained and dependency-free.

`make reviewable` stays green (generate -> no diff, lint 0 issues, unit tests
pass). One incidental change: `go mod tidy` moved `go-cmp` from a direct to an
indirect dependency, because the old generated test (which used `cmp.Diff`) was
replaced — go-cmp is still pulled in transitively, just no longer imported by us
directly.

### A note on naming: "service" -> "client"

The template scaffolds the external dependency with generic *service* names: a
`NoOpService` type, a `newServiceFn` factory on the connector, and an error
string `"cannot create new Service"`. These are placeholders — the template's own
comment even says it "would be something like an AWS SDK client".

We standardised on **client** throughout:
```
NoOpService / newNoOpService  -> apiClient / newAPIClient   (step 13)
newServiceFn                  -> newClientFn
svc (local var in Connect)    -> client
"cannot create new Service"   -> "cannot create API client"
```
Rationale: in networking the object that makes *outbound* calls to a remote API
is conventionally the **client** (vs the server it talks to). Our type literally
wraps Go's `http.Client`, and the Crossplane interface we implement is
`ExternalClient` — so "client" matches both Go convention and Crossplane's own
vocabulary. A "service" would more naturally name the remote thing itself (here,
the mock-apiserver), not the object that calls it.

Lesson: when renaming a struct field, run `gofmt` — shortening `newServiceFn` to
`newClientFn` changed the alignment of the surrounding struct literal, which the
linter flagged as a gofmt diff until reformatted.

## Step 14: the local integration run (the payoff)

Unit tests prove the logic in isolation; this step proves the *whole machine*
runs — a real controller, watching a real cluster, talking to a real (mock) HTTP
backend. Three moving parts, run by hand rather than via `make dev` (which bundles
them and blocks the terminal):

```
mock-apiserver  ->  /tmp/mock-apiserver -addr :8088        (the external system)
kind cluster    ->  provider-learn-dev                     (the API server)
controller      ->  go run cmd/provider/main.go --debug    (out-of-cluster)
```

**Wiring it up.**
1. `kind create cluster --name=provider-learn-dev` — sets the kube context.
2. `kubectl apply -R -f package/crds` — installs the Instance + ProviderConfig CRDs.
3. `kubectl apply -f examples/provider/config.yaml` — the ProviderConfig bundle.
4. **Override the creds secret.** The scaffold ships a dummy base64 blob
   (`BASE64ENCODED_PROVIDER_CREDS`). Our `newAPIClient` parses creds as JSON, so we
   replace it with `{"endpoint":"http://localhost:8088"}`. Because the controller
   runs *on the host* (out-of-cluster), `localhost:8088` reaches the mock directly.
5. `go run cmd/provider/main.go --debug` — starts all controllers.
6. `kubectl apply -f examples/database/instance.yaml` — the trigger.

**What the run proved.** Driving the resource through `kubectl` and watching the
mock server's logs, every `ExternalClient` path fired against the live stack:
```
CREATE   GET 404  -> POST 201        absent -> created, observableField computed
OBSERVE  GET 200                      ResourceUpToDate: true, no further call
UPDATE   GET 200  -> PUT 200  -> GET  patch small->large -> drift -> reconciled
DELETE   GET 200  -> DELETE 204 -> GET 404   gone from both cluster and mock
```
Cluster-side the object reported `READY=True (Available)` /
`SYNCED=True (ReconcileSuccess)`, carried `external-name=example-instance`, and its
`status.atProvider.configurableField` tracked `small -> large`. The controller's
debug log mirrored each transition (`CreatedExternalResource`,
`UpdatedExternalResource`, `External resource is up to date`).

**Gotchas worth remembering.**
- *Reading `atProvider` too early.* Right after a `kubectl patch`, a quick
  `kubectl get` can show the **old** value — `atProvider` only updates on the next
  Observe (the requeue), not synchronously with the spec write. The truth is in the
  reconcile log and the backend, not an instant read.
- *The `PUT` body omits `name`.* Update sends `{"name":"","configurableField":...}`.
  Harmless here because the mock keys off the URL path, but a real API that reads
  `name` from the body would need the client to populate it.
  **Fixed afterwards** (`fix(instance): send name in the update request body`):
  `Update` now sets `Name` from the external-name, and the lifecycle test's PUT
  handler asserts the body carries it so the contract can't silently regress.
- *`make dev` vs by hand.* `make dev` is fine for a one-shot demo, but running the
  three parts separately lets you read each log stream and apply/patch/delete
  between observations — much better for *learning* what each call does.

This is the milestone: scaffold -> build -> mock -> implement -> **verify**, closed.
