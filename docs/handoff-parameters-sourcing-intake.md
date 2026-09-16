---
artifact_contract: "ce-handoff/v1"
created_at: "2026-09-16T12:12:35Z"
title: "inventree-mcp: parameters, sourcing and component intake"
summary: "Issue #1 implemented and closed, PR #2 merged: 42 tools and three silent-API-failure fixes; nothing verified against a live InvenTree instance."
keywords: ["inventree", "mcp", "go", "parameters", "supplier-part", "intake", "part-image", "docker"]
cwd: "repository checkout (captured in an ephemeral remote container)"
resume_focus: "Verify the new tools against the live InvenTree instance and finish the Docker image rebuild"
repository: "iu3qez/inventree-mcp"
repo_root_sha: "284250c0fd518565efbe652ee90a9296d7d3716e"
branch: "claude/adoring-tesla-rw8y50"
head: "d7676290564a5dfe4ee8684f5583150caa36c4a2"
---

# inventree-mcp: parameters, sourcing and component intake

Written at `d767629`, which is now in `master` through merge commit `7259517`. Everything below
describes the state of the code as of `d767629`.

## Objective

Close out [issue #1](https://github.com/iu3qez/inventree-mcp/issues/1): registering a component from a
distributor code required dropping out of the MCP server and hitting the REST API with a separate
script. Points 1–3 (part fields, parameters, manufacturer/supplier data) were in scope. Point 4,
label printing, was excluded — the user's decision, on the grounds that the existing Brother QL setup
via `brother_ql_web` already covers it and moving generation into InvenTree's template engine is a
separate call.

## Current state

The work is complete and pushed. It is **not verified against a live InvenTree instance** — that is
the main open item.

- [PR #2](https://github.com/iu3qez/inventree-mcp/pull/2) merged into `master` as `7259517`. It was
  a merge commit, not a squash, so `300a032` and `d767629` are permanent SHAs in `master`'s history.
- [Issue #1](https://github.com/iu3qez/inventree-mcp/issues/1) closed as completed with a summary comment.
- Tool count 26 → 42.
- `go build`, `go vet`, `gofmt -l` clean; 11 unit tests pass; the 7 pre-existing integration tests
  still skip themselves without `INVENTREE_URL` / `INVENTREE_TOKEN`.

Two commits, deliberately separate:

- `300a032` — issue #1 points 1–3: part fields, parameter tools, sourcing tools, `intake_part`,
  typed `*client.APIError`, and the first unit tests.
- `d767629` — fixes and features ported from [syntaxerr66/inventree-mcp#1](https://github.com/syntaxerr66/inventree-mcp/pull/1)
  (Daniel Glaser): the three silent failures below, plus tags, stock history and price breaks.

## Operational notes the user asked to carry over

These change how the container has to be built and networked. Both were verified against the
InvenTree sources, not against the running instance.

**`set_part_image`, `upload_part_image`, and `intake_part` with `image_url` now download the image
in-process.** InvenTree removed the `remote_image` field from the Part and Company API in **API
v489**; it used to make the InvenTree server fetch the URL itself. DRF drops unknown keys without
complaining, so the old code returned HTTP 200 and left the part with no image — a silent failure,
not an error. The tools now fetch the bytes and PATCH them as `multipart/form-data`
(`attachPartImage` in `internal/tools/parts.go`).

Two consequences for the Docker deployment:

1. **The container needs outbound HTTPS.** Previously only InvenTree reached the image host. If the
   container is confined to the compose network, these three calls fail — loudly, at least, not
   silently.
2. **The final image needs `ca-certificates`.** The Go binary now makes HTTPS requests to arbitrary
   external hosts; on a bare `alpine` or `scratch` image it dies with
   `x509: certificate signed by unknown authority`. The base image (`ghcr.io/sparfenyuk/mcp-proxy`)
   almost certainly ships them already, but it is now a runtime dependency of the binary rather than
   an accident of the base, so it is worth declaring: `RUN apk add --no-cache nodejs ca-certificates`.

Also relevant to the image build: the `go.mod` module path is `github.com/chrisbotelho/inventree-mcp`
while the fork lives under `iu3qez`, so `go install` fails the module path check and clone + build is
the only route. Pin `INVENTREE_MCP_REF` to `d7676290564a5dfe4ee8684f5583150caa36c4a2`, which reaches
`master` through the merge commit and stays valid whether or not the branch is deleted. The rewritten Dockerfile was produced in the session but belongs to the
MCP-proxy image, not to this repository, so it is not saved here or anywhere else on disk.

## The other two silent failures

Both fixed in `d767629`, both previously returning success while doing nothing:

- **`delete_stock_location` and `delete_part_category` could not delete anything.** Both endpoints put
  required fields on the delete serializer and reject a body-less DELETE. They now send the
  confirmation body via `client.DeleteWithBody`, and since the container need not be empty, the result
  reports whether contents were deleted with it or moved to the parent.
- **Company creation was missing `currency`**, which InvenTree requires but does not default on the
  API. `defaultCurrency` in `internal/tools/companies.go` reads it from
  `/api/settings/global/INVENTREE_DEFAULT_CURRENCY/`. This one was a bug in the first commit's own new
  code, caught by reading the upstream PR.

## Decisions and rejected alternatives

- **Cherry-pick over merge (the user's choice).** The upstream PR is ~3350 lines and 49 tools, with a
  fuller CRUD surface on companies/supplier/manufacturer parts. The alternative considered and
  rejected was merging it wholesale and re-applying `intake_part` on top, which meant resolving
  conflicts in `parameters.go`, `companies.go` and `parts.go`. What was taken instead: the client
  primitives, the image path, the delete fixes, tags, stock history, price breaks. What was left
  behind: update/delete tools for companies, supplier parts and manufacturer parts, and their
  integration tests.
- **Legacy parameter API fallback (my call, not asked for).** API v430 replaced `/api/part/parameter/`
  with the generic `/api/parameter/` endpoints addressed by `model_type` + `model_id`. The target
  instance is API 530, so the fallback is dead weight for this user specifically; it was kept so the
  server works against pre-v430 instances. The flavour is probed once per run and cached; a probe
  failing for any reason other than 404 is deliberately not cached.
- **Unit tests against a fake InvenTree server (my call).** The pre-existing suite is integration-only
  and skips itself entirely when no instance is configured, which is why the three silent failures had
  gone unnoticed. `internal/tools/intake_test.go` is `package tools` (internal) so it can reach
  unexported symbols; the existing `tools_integration_test.go` stays `package tools_test`.

## Where to look

Repository-relative, on branch `claude/adoring-tesla-rw8y50` at `d767629`:

- `internal/tools/parameters.go` — `paramAPIResolver.resolve` is the v430/legacy probe; `findTemplate`
  tries the exact `name` filter before the fuzzy `search` pass and always confirms the match locally.
- `internal/tools/companies.go` — `getOrCreateCompany` is the idempotent path used by `intake_part`;
  `ensureCompanyRoles` widens roles and never removes them.
- `internal/tools/intake.go` — `intake_part`; note that steps are independent and failures accumulate
  in `problems` rather than aborting, except a failed part creation which is fatal.
- `internal/tools/parts.go` — `attachPartImage` and `fetchImage` are the multipart upload path; the
  comment above `attachPartImage` records how `remote_image`'s removal was verified.
- `internal/client/client.go` — `PatchMultipart`, `DeleteWithBody`, and `APIError` / `StatusCode`,
  which exists so callers can branch on a 404 without matching error strings.
- `internal/tools/intake_test.go` — `fakeInvenTree` is the stand-in server; add a case to `handle` when
  a new endpoint is exercised.
- `CLAUDE.md` — the InvenTree API section now records the v430, v434, v489 and currency gotchas.

External: [PR #2](https://github.com/iu3qez/inventree-mcp/pull/2),
[issue #1](https://github.com/iu3qez/inventree-mcp/issues/1),
[upstream PR](https://github.com/syntaxerr66/inventree-mcp/pull/1).
API behaviour was verified by reading `src/backend/InvenTree/InvenTree/api_version.py` and the
serializers in the InvenTree repository at `master`; those working copies were temporary and are gone.

## Unfinished, and known rough edges

- **Nothing has been exercised against the live instance.** Endpoints and field names were checked
  against the InvenTree sources only. As of the end of the session the MCP server in the user's
  environment was exposing the new tools (`intake_part`, `upload_part_image`, `set_part_parameters`
  and the rest), which suggests the binary had already been rebuilt — unconfirmed.
- **The issue #1 closing comment says label printing is "tracked separately", but no follow-up issue
  exists.** My wording, not the user's. Either open one or edit the comment; `mcp__github__update_issue_comment`
  is available for the edit.
- **Nested arguments are still not coerced.** `internal/coerce/coerce.go` only walks top-level
  arguments, so a client sending `items: [{"pk": "42"}]` to `stock_add_quantity`,
  `stock_remove_quantity` or `stock_transfer` still fails schema validation. Found early in the
  session, never in scope, never fixed.
- **`go.mod` is not tidy** — every dependency is marked `// indirect` though they are direct.
- **Module path / repo mismatch**, as above; the README also still points at
  `syntaxerr66/inventree-mcp` for cloning.
- **Not ported from upstream**: update/delete for companies, supplier parts, manufacturer parts, and
  the upstream integration tests.

## Verification performed

`go build ./...`, `go vet ./...`, `gofmt -l .` all clean at `d767629`. `go test ./...` passes: 11 unit
tests against the fake server covering parameter API flavour detection (both flavours, caching, and
that a transient failure is not cached), template creation and reuse, `create_templates=false`,
company idempotency and role widening, the intake workflow end to end and under partial failure, and
the image upload path including rejection of a non-image URL. Failures observed and fixed during the
session were all in test scaffolding — a prefix-matching call counter that conflated
`/api/company/part/` with `/api/company/part/manufacturer/`, and a fake server missing the currency
setting endpoint — except the missing `allowCreate` parameter, which was a real compile error in
`parameters.go`.

## Plausible next steps

1. **Exercise the new tools against the live instance**, which is the only verification that has not
   happened. `search_supplier_parts` and `get_part_parameters` are cheap reads to start with;
   `intake_part` against a throwaway category is the real test, and `set_part_image` is the one most
   likely to surface a container networking or CA problem.
2. **Rebuild the Docker image** with the ref pinned to `d767629` and `ca-certificates` declared.
3. Optionally, pick up any of the rough edges above — the nested-argument coercion gap is the only one
   that is a live bug rather than tidiness.
