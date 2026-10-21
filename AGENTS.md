# AGENTS.md

Detailed context for AI agents working on this codebase. [README.md](README.md) is the source of truth for what the plugin does and how it is used.

## What This Project Is

A **Mattermost plugin that creates plugin-managed user and channel attributes and fills them in from a JSON file.** It exists to set up test environments that need attributes the System Console cannot create: `protected` attributes, and the `source_only` and `shared_only` access modes that are only available on them. The attributes appear on user profiles in the UI and are addressable from attribute-based access control (ABAC) policy rules as `user.attributes.<field_name>` (user attributes) and `resource.attributes.<field_name>` (channel attributes) — which is why the plugin writes into the `access_control` property group.

It is a fork of `mattermost-plugin-user-attribute-sync-starter-template` and is no longer a template. Guidance written for someone building their own plugin does not belong here; guidance for someone loading attributes into a test server does.

It ships **one data source**: a JSON file uploaded through the System Console into the plugin KV store, read by `ReadStoredAttributes`. Uploading is the only way to give the plugin data — a filesystem source would be awkward or impossible to deploy in some environments anyway, since Cloud installations give no direct filesystem access and container filesystems are ephemeral, so a file placed next to the server would not survive a restart. Supporting the upload is what pulls in the rest of the machinery: a custom admin console setting, a plugin HTTP API, and server-side storage.

**Plugin ID:** `com.mattermost.user-attribute-sync-test-tool`
**Min Mattermost version:** 12.0.0 (the `graph` field type requires it). Until a v12 `server/public` is published, `go.mod` reads that module from a local checkout of the `dk-graph-property-fields` branch through a `replace` directive — never hand-edit that checkout, it is read-only reference.
**License:** Enterprise Advanced, for ABAC. Every field and value operation on the `access_control` group requires a license, so the plugin fails at activation without one. Channel fields need this tier specifically: below it every channel field is skipped and counted in `FieldsSkipped`, and the rest of the document still syncs.
**Languages:** Go 1.26.3+ (server), TypeScript/React (webapp)

## Architecture

```text
Plugin Activation (Once)
  ├─> Register HTTP routes (mux router on ServeHTTP)
  └─> If a document is stored: SyncDocument under syncLock (fields then values)
      With no document: skip (ErrNoStoredDocument)

System Console (Admin, on demand)
  └─> Custom "Attributes" setting (webapp)
        └─> Upload / download / delete the stored file
              └─> Plugin HTTP API ──> KV store, then SyncDocument (upload) or field wipe (delete)
                    └─> Response is the sync summary the panel renders
```

The plugin has one sync path, `SyncDocument`:
1. **Field sync** — If a document is stored (or just uploaded), applies `fields.template`, then `fields.user` and `fields.channel`, as this plugin's schema (create/update, and delete plugin-owned fields the document omits). Templates sync first because a linked field cannot be created before its template exists, and they are not channel-object fields, so the Enterprise Advanced gate below does not apply to them. With no document, activation skips entirely. Channel fields are gated on an Enterprise Advanced license: below that tier every channel field is skipped and counted in `FieldsSkipped`, and the rest of the document still syncs.
2. **Value sync** — Writes per-user and per-channel values from the same document, using the field-ID caches from the field passes.

Activation calls `runSync()`, which reads the stored document and runs `SyncDocument` under `syncLock`. Upload runs `SyncDocument` on the document it just parsed, under the same lock, so two triggers cannot interleave their property-service writes. The System Console setting is the UI for that API: upload stores then syncs and shows the summary; delete is a full reset.

All fields and values are stored in the `access_control` property group (`model.AccessControlPropertyGroupName`), with `ObjectType=user`, `ObjectType=channel`, or `ObjectType=template` and `TargetType=system`. Living in that group is what makes the fields addressable from ABAC policy expressions.

## Key Files and Their Roles

### Server (Go)

| File | Role |
|------|------|
| `server/plugin.go` | Plugin struct, `OnActivate`. Initializes the API router, looks up the property group, and calls `runSync()`. |
| `server/http_hooks.go` | `ServeHTTP` + `initializeAPI()`. The `gorilla/mux` router and the four `/attributes` handlers, the sysadmin permission check, and the JSON response helpers. Upload stores then syncs; delete wipes plugin-owned fields. |
| `server/job.go` | `runSync()` — the activation path. Reads the stored document, parses it, calls `sync.SyncDocument()`. Holds `syncLock` for the whole read-and-sync. |
| `server/configuration.go` | Thread-safe config management with RWMutex. The struct has no settings; `OnConfigurationChange` still loads it. |
| `server/sync/field_sync.go` | Schema reconciliation from the stored document's `fields.template`, `fields.user`, and `fields.channel`. Creates/updates fields for the given object type and, after all passes, deletes plugin-owned fields the document omits (`DeleteOmittedFields`, keep-list keyed by object type and name; linked fields are deleted before templates, which the server will not delete while dependents remain). A field carrying a `template` key is created linked: the server copies type, options, and access mode from the template, and a link-state change on an existing field is a delete-and-recreate. Maintains `FieldIDCache` mapping external names to Mattermost-generated IDs and declared types. Graph options are written inline, with their `parents` name links, in the same field write. Counts created/updated/deleted/skipped on `Summary`. |
| `server/sync/value_sync.go` | `SyncUsers()` and `SyncChannels()` — match by email or by team plus channel name, build PropertyValue objects, bulk upsert. Handles text, date, multiselect, graph, and rank value types. Counts synced and skipped users and channels on `Summary`. |
| `server/sync/sync.go` | `Summary` and `SyncDocument()` — template, user, then channel field sync, one deletion pass, then user then channel values. Channel field sync is skipped below Enterprise Advanced; template sync is not. |
| `server/sync/document.go` | `ParseAttributesDocument()` decodes the uploaded bytes into `version`, `fields.template`, `fields.user`, `fields.channel`, `users`, and `channels`, rejecting anything that is not a JSON object or carries an unsupported `version`. The one parser shared by the upload handler and `runSync`, so the two cannot disagree about what a valid document is. |
| `server/sync/kv_store_provider.go` | Owns the KV key (`AttributesStoreKey`), the `StoredAttributes` value stored under it, `ReadStoredAttributes`, and `ErrNoStoredDocument`. |
| `server/main.go` | Plugin entry point (minimal). |
| `server/manifest.go` | Auto-generated from plugin.json — do not edit manually. |

### Webapp (TypeScript/React)

The webapp exists only to render the custom `Attributes` setting in the System Console. There is no channel/RHS/post UI.

| File | Role |
|------|------|
| `webapp/src/index.tsx` | Plugin registration. `registerAdminConsoleCustomSetting('Attributes', UploadUserAttributes, {showTitle: true})` — the one hook this plugin uses. |
| `webapp/src/components/upload_user_attributes.tsx` | The upload panel, mounted directly as the custom setting. Client-side validation (10 MB cap, must parse as a JSON object carrying the supported `version`), then upload/download/delete against the plugin HTTP API. Probes `/attributes/status` on mount to show whether a document is already stored and when it was uploaded. After upload or delete, renders the sync summary from the response body. Takes `disabled` but deliberately not `setByEnv` — see Admin Console Setting below. |
| `webapp/src/components/confirm_modal.tsx` | Local `react-bootstrap` confirm dialog, used to gate deletion. |
| `webapp/src/components/*.scss` | Styles for the above. `webapp/src/types/scss.d.ts` declares `*.scss` so TypeScript accepts the side-effect imports. |
| `webapp/src/manifest.ts` | Auto-generated from plugin.json — do not edit manually. |

### E2E (Playwright)

`e2e/` is a standalone npm project (its own `package.json`, `tsconfig.json`, `.eslintrc.json`) driving the System Console setting through a real browser against a real server. `e2e/README.md` is the authoritative guide — prerequisites, layout, and the conventions it holds itself to (page objects only, accessibility-first locators, API-driven setup, `workers: 1` because saving admin console settings is a read-modify-write of the server-wide config).

| Path | Role |
|------|------|
| `e2e/constants.ts` | Base URL, admin credentials, plugin ID, fixture paths. Mirrors constants in `plugin.json`. |
| `e2e/utils.ts` | REST helpers — login, enable plugin, patch a plugin setting, upload/delete the stored file. |
| `e2e/global-setup.ts` / `global-teardown.ts` | Log in once, save the admin storage state, enable the plugin; remove the state file afterwards. |
| `e2e/pages/plugin_settings_page.ts` | The only page object. All locators for the plugin's settings section live here. |
| `e2e/tests/settings.spec.ts` | The upload panel renders. |
| `e2e/tests/attributes.spec.ts` | Upload/download/delete round trips, invalid-document rejection, delete-confirmation behaviour, sync summary on upload and reset. |
| `e2e/assets/` | Deliberately invalid fixtures. The valid fixture is the repo's own `data/attributes.json`. |

### Build & Config

| File | Role |
|------|------|
| `plugin.json` | Plugin manifest: ID, version, executables, settings schema. |
| `Makefile` | Build orchestration (~420 lines). All build/test/deploy commands. |
| `build/setup.mk` | Extracts PLUGIN_ID, PLUGIN_VERSION, HAS_SERVER, HAS_WEBAPP from plugin.json. |
| `build/pluginctl/` | Tool for local Mattermost deployment. |
| `data/attributes.json` | Example attributes document: a `clearance` template in `fields.template`, a linked user field of the same name, three more `fields.user` entries, and sample user records. Channel fields are documented in README.md rather than here, so e2e field-count assertions do not depend on license tier. |

## Field Definitions

Come from `fields.template`, `fields.user`, and `fields.channel` in the uploaded document — same field-definition shape on all three lists. `data/attributes.json` is the example user set (same four attributes a test environment typically wants); channel examples live in README.md:

1. **Job Title** — `job_title`, Text type, Public access
2. **Programs** — `programs`, Multiselect type, SharedOnly access, options: Apples/Oranges/Lemons/Grapes
3. **Clearance** — `clearance`, a Rank template (SharedOnly access, options: CUI(1)/Confidential(2)/Secret(3)/Top Secret(4)) with a linked user field of the same name
4. **Start Date** — `start_date`, Date type, SourceOnly access

A user or channel definition links to a template by carrying a `template` key naming an entry in the same document's `fields.template`. The linked field serves the template's option set, so a user field and a channel field linked to one template share option IDs — which is what lets an ABAC policy compare `user.attributes.<name>` against `resource.attributes.<name>`. On a linked definition `type`, `options`, and `access_mode` are ignored (the server copies them from the template); `visibility` and the `permission_*` keys still apply. Linking is optional — a definition with no `template` key behaves as it always has. Templates hold no values: a template name used as a key in a `users` or `channels` record is an unknown field, skipped with a warning. Name uniqueness is per object type, so a template, a user field, and a channel field may share a name. A `template` key naming a template the document does not define fails that one field — logged, counted in `FieldsSkipped`, rest of the document still syncs.

`protected` is always `true` and is not a document field. `visibility`, `access_mode`, `permission_field`, `permission_values`, and `permission_options` are optional and default to `always`, public, and `sysadmin`. Accepted values: visibility `always` / `hidden` / `when_set`; access mode `public` / `source_only` / `shared_only`; permission levels `none` / `sysadmin` / `member` / `admin`. `shared_only` with `permission_values: member` is rejected by the server; that field is skipped and logged, and the rest of the document still syncs.

Display actions (`attrs.actions`) are not a document field either: `createField` and `updateField` set `displayActions` — top banner, channel header label, channel info label — on every field, whatever its object type. This is deliberately hardcoded for now; giving the document a per-field `actions` key is the change to make if a test ever needs a subset.

A `graph` field's options may each carry a `parents` list naming the options directly above them, resolved by name within the same `options` list — so option names must be unique, and graph options cannot carry `rank`. A graph value is a list of option names, written like a multiselect value. The server refuses the field when its `PropertyFieldGraph` feature flag is off; a refused field is skipped and logged, and the rest of the document still syncs.

The uploaded document is the full list of fields this plugin owns: a plugin-owned field it omits is deleted with its values. This is per object type: a document that defines `fields.user` but no `fields.channel` deletes the plugin's channel fields, and the reverse — and `fields.template` is the full list of templates the same way. Linked fields are deleted before templates, because the server refuses to delete a template that still has linked fields; if a dangling `template` reference leaves such a dependent behind, the template delete fails, is logged, and both fields stay. Fields owned by an admin or another plugin are left alone. It is not the full list of values — a user or channel the document does not mention keeps existing values; values disappear only when their field does. With no document stored, activation skips field sync, so a restart cannot delete attributes.

Field types cannot be changed after creation (a Mattermost limitation), and conversions to or from `graph` are refused outright. To change one, upload a document that omits the field (deletes it and its values), then upload again with the new type. A field's link state cannot change in place either: linking a previously unlinked field, or unlinking a linked one, deletes and recreates the field — its values are lost, and an ABAC policy referencing it breaks until re-pointed.

### Access Modes

- **Public**: Everyone can read all values
- **SharedOnly**: Users only see values they share with the target (select, multiselect, and rank only; on rank, a user sees their own rank and lower)
- **SourceOnly**: Only this plugin can read values via API

## Data Flow

1. `OnActivate()` calls `initializeAPI()` to build the HTTP router, then `runSync()`
2. `runSync()` holds `syncLock`, reads the stored document via `ReadStoredAttributes`, parses it, and calls `SyncDocument()`. No document is `ErrNoStoredDocument` — activation logs and continues; the HTTP routes stay up.
3. `SyncDocument()` runs `SyncFields()` for templates, then users, then channels if licensed, then `DeleteOmittedFields`, then `SyncUsers()` and `SyncChannels()`, accumulating a `Summary`. Templates come first so a linked field can be created in the same upload, and the template pass's name-to-ID map is handed to both later passes to resolve `template` keys. Each field pass has its own field-ID cache, used immediately for that object type's values and not kept on the plugin. Below Enterprise Advanced the channel field pass is skipped (`FieldsSkipped`) and `SyncChannels` still runs against an empty cache, so channel values land in `ChannelsSkipped`.
4. `SyncUsers()` iterates users, looks up each by email, calls `buildPropertyValues()` to create `PropertyValue` objects, then bulk upserts via `Property.UpsertPropertyValues()`. `SyncChannels()` does the same after `Channel.GetByNameForTeamName(team, channel)`.
5. For fields with options (select, multiselect, rank, graph), option names are translated to option IDs using `FieldIDCache`

Upload skips the KV read: `handleUploadAttributes` stores the bytes, then runs `SyncDocument()` under `syncLock` on the document it already parsed, and answers `201` with the `Summary`. Re-reading the store would not be safe — plugin KV reads can hit a database replica that has not yet seen the write, which looks like an empty store. `handleDeleteAttributes` takes the same lock, deletes the KV key first, then `DeleteOmittedFields` with an empty keep-list so every plugin-owned field (and its values) is removed.

**Invariants worth knowing before changing sync code:**

- `email` is the join key between external data and Mattermost users; `team` and `channel` are the join keys for channel records. They are consumed by `SyncUsers()` / `SyncChannels()` and listed as identity keys so `buildPropertyValue()` never writes them as attributes. Changing the identity strategy means touching both.
- A `FieldIDCache` is built per object type during field sync inside `SyncDocument` and used immediately for that type's value sync. External names → Mattermost-generated field IDs; option names → option IDs; `FieldNameToType` holds the type read back from the server-returned field — for a linked field that is the template's type, and its option IDs are the template's, so value sync writes option IDs the user and channel sides of one template share. A cache miss on a field name is a skip-with-warning; a cache miss on an *option* name is an error for that value.
- Value JSON shape is type-dependent: text and date are marshaled strings; multiselect and graph are arrays of option **IDs**, not names; rank is a single option **ID** string, so a rank value looks like a text value on the wire and is only distinguishable by consulting the field definition.
- `FieldNameToType` on `FieldIDCache` is how value sync recovers a field's declared type from the document's key. Adding an option-bearing field type means updating the value-formatting switch in `buildPropertyValue()` together with how field sync records the type, or values will be written as raw names instead of option IDs.
- Failure handling is per-record and per-field: unknown fields, unsupported value types, format errors, missing users or channels, and upsert failures all log and continue. `SyncUsers()` and `SyncChannels()` return `nil` unless something structural goes wrong — a "successful" sync can have written nothing. Those skips still increment `UsersSkipped` / `ChannelsSkipped` / `FieldsSkipped` on the summary.
- `syncLock` serializes `runSync()` (activation), the upload handler's sync, and `handleDeleteAttributes`. In `runSync()` the lock covers the stored-document read as well as the property-service writes, so two triggers cannot interleave.
- An upload stores the document, then syncs the parsed request body (never a KV re-read), and the response body is the `Summary` the console renders. Per-field and per-record failures are counted on that `Summary` and still answer `201`. A stored document whose sync then fails is still stored (`500`, "document stored but sync failed") — activation will retry it.
- `ErrNoStoredDocument` is the empty-store case (`len(Data) == 0`), not a parse error. Activation, `runSync`'s only caller, treats it as skip-with-info.
- One KV key, `AttributesStoreKey` (`attributes`), holds a `StoredAttributes` — the uploaded bytes and the timestamp the status endpoint reports, written and deleted as one value. `ReadStoredAttributes` is the only reader: it decodes the raw value itself rather than letting `KV.Get` unmarshal in place, which would surface an unreachable store and a corrupt value as the same error. An unset key is neither — `KV.Get` returns no error and leaves the target untouched, so "nothing uploaded" is the `len(Data) == 0` case.
- `pluginapi`'s `KV.Set` stores raw bytes only when handed a `[]byte` and JSON-encodes anything else, so the document is base64 inside the envelope — roughly a third larger in the database than on disk. `json.RawMessage` would avoid that, at the cost of `json.Marshal` compacting the JSON and so breaking the byte-for-byte download.
- **`ParseAttributesDocument` is the single place that decides what a valid document is**, shared by the upload handler and the sync path, so the two cannot drift into accepting different things. It decodes `version`, `fields.template`, `fields.user`, `fields.channel`, `users`, and `channels`; omitted lists stay empty.

## HTTP API

Registered in `server/http_hooks.go` on a `gorilla/mux` router, served through the `ServeHTTP` plugin hook. Full paths are prefixed `/plugins/com.mattermost.user-attribute-sync-test-tool`.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/attributes` | Upload the attributes document into the KV store, then sync it. Body is the raw JSON document. `201` is the `Summary`. Store-then-sync-fail is `500` with the document still stored. |
| `GET` | `/attributes` | Download the stored document verbatim. `404` when nothing is stored. |
| `GET` | `/attributes/status` | `{"exists": bool, "lastUpdated": time\|null}` — lets the UI show whether a document is stored, and how current it is, without downloading it. |
| `DELETE` | `/attributes` | Remove the stored document, then every field this plugin owns. `200` is the `Summary` (field-delete counts filled in). |

Things to preserve when changing these:

- **The sysadmin check is router middleware, not per-handler.** `initializeAPI` applies `requireSysadmin` via `router.Use`, so a route added later is protected by default rather than depending on the author remembering. It reads the `Mattermost-User-Id` header — the only header the plugin can trust, because the Mattermost server sets it on the way in and strips any client-supplied value — and requires `model.PermissionManageSystem`. Two consequences: the whole router is admin-only, so a genuinely public route needs the protected routes moved to a subrouter with `Use` applied there; and gorilla/mux builds the middleware chain only on a matched route (`Router.Match`), so unknown paths 404 without reaching the check.
- **Uploads are bounded twice.** `http.MaxBytesReader` caps the body at `maxFileSizeBytes` (10 MB) server-side, and the UI checks `MAX_FILE_BYES` (also 10 MB) before sending. The two constants are independent — keep them in step.
- **Validation is shape-only, on both sides, by design.** Server and client both require the payload to unmarshal as a document carrying the supported `version`; neither validates emails, field names, or value types. This is not a gap to close: external data routinely contains a few unusable records, and rejecting the whole document over one of them would mean syncing nothing. Per-record failures are already handled where they belong, in value sync, which logs and continues (see the failure-handling invariant above). Adding record-level validation to the upload endpoint would move that decision to the worst possible place — an all-or-nothing gate in front of otherwise good data.
- The webapp reaches these routes with plain `fetch`, using `Client4.getOptions()` on `POST`/`DELETE` to pick up the CSRF token. `GET`s need no options.

## Admin Console Setting

`plugin.json` declares `Attributes` as `"type": "custom"`, which tells the System Console to render a webapp-supplied component instead of a built-in control. `index.tsx` supplies it via `registry.registerAdminConsoleCustomSetting`, mounting `UploadUserAttributes` directly — the setting holds no value of its own; it exists only because `registerAdminConsoleCustomSetting` needs a `type: custom` entry to mount the panel at all.

- The console passes considerably more props than this component declares (`label`, `helpText`, `config`, `license`, `registerSaveAction`, `setSaveNeeded`, `showConfirm`, …). `upload_user_attributes.tsx` deliberately takes only `id` and `disabled`; the rest are available if a future version needs them (see `schema_admin_settings.tsx`'s `buildCustomSetting` in the server repo). It does not declare `setByEnv` — there is nothing to forward, since the setting holds no value an environment variable could pin.
- The upload/download/delete buttons hit the plugin API directly and take effect immediately; there is no Save button involved, because the setting holds no value for the console to save. Upload and delete responses carry the sync summary, which the panel lists.
- Because `showTitle: true` is passed at registration, the console wraps the component in its own `Setting` (rendering `display_name` as the label and `help_text` beneath it).
- If the plugin is disabled, the webapp component is not registered, and the console renders a warning banner ("In order to view this setting, enable the plugin and click Save") in place of the setting. A blank-looking setting usually means the bundle failed to load, not that the component is broken.

## Adding to the Plugin

### A new attribute
Add an object to `fields.user` or `fields.channel` in the uploaded document and upload it. Select, multiselect, rank, and graph types also need `options` populated; on a rank field every option needs a `rank`, which `buildOptionsArr()` enforces.

### Server-side rules to work within
Enforced by the property service, not by this plugin, so they cannot be worked around from here:

- Every field and value operation on the `access_control` group requires an Enterprise license.
- `protected` and `source_plugin_id` can only be set by a plugin; `source_only` and `shared_only` require `protected`.
- `source_plugin_id` cannot be changed after creation, so fields created under a different plugin ID cannot be adopted by this one — `syncSingleField()` fails with "already exists but is not managed by this plugin".
- While the plugin is installed, only it can delete its own protected fields.

## Build & Test Commands

```bash
make                    # Full build: check-style + test + dist
make test               # Run all tests (Go gotestsum + Node jest). Does NOT run e2e.
make test-e2e           # Playwright e2e — needs a running server with the plugin deployed
make check-style        # golangci-lint + eslint + type checking, for webapp AND e2e
make server             # Compile Go binaries only
make webapp             # Build webapp bundle only
make dist               # Build and create tar.gz bundle
make deploy             # Build + deploy to running Mattermost
make watch              # Rebuild the webapp bundle on change (does not deploy)
make deploy-from-watch  # Install the bundle make watch rebuilt
make clean              # Remove all build artifacts
make install-go-tools   # Install golangci-lint v1.64.8, gotestsum v1.7.0 into $GOBIN
make coverage           # Go coverage profile + open HTML report
make logs / logs-watch  # Fetch / tail plugin logs on running server (via build/pluginctl)
make attach             # Attach delve to the running plugin process (attach-headless, detach)
make disable / enable / reset / kill   # Plugin lifecycle on the running server
make apply              # Regenerate manifest files from plugin.json
make patch|minor|major  # Bump plugin.json version (also *-rc variants)
```

**Running a single test:**

```bash
cd server && go test ./sync/ -run TestSyncUsers -v          # one Go test
cd server && go test ./sync/ -run 'TestReadStoredAttributes_.*' -v  # pattern
cd server && go test . -run 'TestHandle.*' -v               # the HTTP handler tests (package main)
cd webapp && npx jest src/manifest.test.tsx                 # one webapp test
cd e2e && npm test -- tests/settings.spec.ts                # one Playwright spec
cd e2e && npm test -- -g 'renders the upload panel'  # by title
```

`make test`/`make check-style` first run `apply`, install Go tools, and `npm install` — going through `go test` directly is much faster for iteration.

`check-style` now also lints and type-checks `e2e/` (and so depends on `e2e/node_modules`), but that happens inside the `HAS_WEBAPP` guard — a fork that removes the webapp silently stops linting the e2e tests too. `make test-e2e` is separate from `make test` and from CI, because it needs a live server; see `e2e/README.md`.

**Generated files:** `server/manifest.go` and `webapp/src/manifest.ts` are produced by `./build/bin/manifest apply` (run automatically by most Makefile targets). Edit `plugin.json`, then `make apply` — never edit the manifest files by hand. `make manifest-check` validates the manifest.

**Go module path:** `github.com/mattermost/mattermost-plugin-user-attribute-sync-testing` (internal imports use `.../server/sync`, aliased `attrsync` in `plugin.go`). `.golangci.yml`'s `goimports.local-prefixes` matches it, so import grouping for local packages is enforced.

**Environment variables:**
- `MM_DEBUG=1` — Debug build (disables optimizations)
- `MM_SERVICESETTINGS_ENABLEDEVELOPER=1` — Build for current platform only (faster)
- `MM_SERVICESETTINGS_SITEURL` — Server URL for deploy
- `MM_ADMIN_TOKEN` — Admin token for deploy
- `MM_SITE_URL` — Server the e2e tests target (default `http://localhost:8065`)
- `MM_ADMIN_USERNAME` / `MM_ADMIN_PASSWORD` — e2e admin credentials (default `sysadmin` / `Sys@dmin-sample1`, what `make test-data` creates in a server checkout)

## Testing Patterns

**Go tests** (`server/*_test.go`, `server/sync/*_test.go`):
- Framework: testify (assert, mock, require)
- Mocking: `plugintest.API` and `plugintest.Driver` from Mattermost
- Pattern: Table-driven tests with `t.Run()`
- Mock expectations with `.On()` and `.Return()`
- Each file has a `newTest*` helper that builds the subject against a fresh `plugintest.API` and registers `t.Cleanup(func() { api.AssertExpectations(t) })`, so unmet expectations fail the test automatically. Follow that shape for new tests.
- `http_hooks_test.go` drives handlers through `p.ServeHTTP` with `httptest`, the same way the server does, rather than calling handler functions directly — so route registration and the permission check are covered too. It sets the `Mattermost-User-Id` header to simulate an authenticated request and mocks `HasPermissionTo` to control authorization.
- `job_test.go` covers `runSync()`: no stored document is `ErrNoStoredDocument`, and a stored document reaches `SyncDocument()`.

**Webapp tests** (`webapp/src/**/*.test.tsx`):
- Framework: **Jest 29 + React Testing Library**, matching the majority of Mattermost plugins (calls, github, gitlab, jira, zoom). Query by role, assert on what the admin can see and do; `@testing-library/jest-dom` matchers are registered globally in `tests/setup.tsx`.
- **RTL is pinned to 12.1.5, and cannot be upgraded while this plugin is on React 17.** RTL 13+ declares `peerDependencies: react >=18`. Every RTL-using Mattermost plugin is on React 18 and therefore on RTL 14.x — do not copy their pin without moving React first.
- Enzyme was removed (`enzyme`, `@types/enzyme`, `enzyme-adapter-react-17-updated`, `enzyme-to-json`). It had been an unconfigured devDependency since the initial commit, inherited from the upstream template, which has since dropped it too. The `snapshotSerializers` entry went with it.
- **Two jest resolution problems had to be solved before any component test could run**, and both are worth understanding before adding tests:
  - `mattermost-redux` (and `@mattermost/client` beneath it) expose subpaths via the `exports` field. Jest 27 predates `exports` support, so `mattermost-redux/client` was unresolvable and *nothing* importing `Client4` could be tested. **Upgrading to jest 29 fixed this natively** — no `moduleNameMapper` needed, which is why the other plugins' configs have no entry for it. This is what jest 27 was costing.
  - `react-bootstrap` is a **webpack external** — the host webapp supplies it at runtime, so it is deliberately not installed and jest cannot resolve it. `tests/react_bootstrap_mock.tsx` stands in for it via `moduleNameMapper`, implementing only the `Modal` surface `confirm_modal.tsx` uses. Any future external in `webpack.config.js` that is not also a real dependency needs the same treatment.
  - **Installing react-bootstrap as a devDependency instead was tried and rejected**, and not for bundle-size reasons — `externals` excludes it by configuration, so an install would cost the bundle nothing. It was rejected because (a) Mattermost's webapp uses its own fork, `github:mattermost/react-bootstrap#05559f4c`, so an upstream install would test against a different library than production; and (b) upstream `0.32.4` — the line matching that fork and the pinned `@types/react-bootstrap@0.32.37` — declares `peer react: >=15.3.0`, which npm satisfies with react-dom 19 and then fails against react 17. `--legacy-peer-deps` does install it, but removes `react-dom` from the tree and breaks RTL; the alternative is an `overrides` pin, i.e. exactly the cruft removed with enzyme. The real Modal is covered in `e2e/` against the host's actual fork.
- `upload_user_attributes.test.tsx` renders the **real** component tree, so the assertions are behavioural rather than prop-level. The mount-time `/attributes/status` request is stubbed with a `fetch` mock; `renderPanel` awaits an `act()` flush so the resulting state update does not land after the test ends.
- `react_fragment.test.tsx` was deleted: it asserted `React.version === '17.0.2'`, so it tested nothing about this plugin while guaranteeing a failure on any React upgrade. `manifest.test.tsx` is kept as a smoke test that manifest generation ran.

**E2E tests** (`e2e/tests/*.spec.ts`):
- Framework: Playwright, `workers: 1`, no server started for you
- Read `e2e/README.md` before touching these. The conventions that matter: locators live only in `pages/`, `getByRole`/`getByText` are preferred over `getByTestId`, and per-test setup goes through the REST helpers in `utils.ts` rather than the UI.

## Code Conventions

- **Error handling:** `errors.Wrap(err, "context")` / `errors.Wrapf()` from `github.com/pkg/errors`
- **Logging:** `p.client.Log.Info/Error/Warn/Debug("message", "key", value)`
- **Thread safety:** RWMutex for configuration, defensive cloning before updates
- **Graceful degradation:** Continue on partial failures; never fail entire sync for one user
- **HTTP responses:** JSON via the `errorWithJSON` / `responseWithJSON` helpers in `http_hooks.go`, not hand-written `w.Write`. Download is the deliberate exception — it streams the stored bytes verbatim.
- **Webapp:** function components with hooks, no Redux, literal strings in JSX braces per the Mattermost eslint config, and no i18n — strings are inline rather than translated.
- **No license headers.** Do not add `// Copyright (c) …` / `// See LICENSE.txt …` blocks to any file. No source file in this repo carries one, and `webapp/.eslintrc.json` sets `"header/header": "off"`. Licensing lives in `LICENSE` at the repo root.
- **No template framing in comments.** Comments should not address a reader who is about to fork the repo or build their own plugin; that framing was swept out of the codebase and should not come back.

## Mattermost API Surface

Used via `pluginapi.Client`:

- `Property.GetPropertyGroup(name)` — Fetch the `access_control` property group
- `Property.SearchPropertyFields(groupID, opts)` — Page fields in the group; `ObjectTypes` filters user vs channel so a shared name does not collide
- `Property.CreatePropertyField(field)` — Create field (returns generated ID)
- `Property.UpdatePropertyField(groupID, field)` — Modify existing field
- `Property.UpsertPropertyValues(values)` — Bulk write attribute values
- `Property.DeletePropertyValuesForField` / `DeletePropertyField` — Wipe an omitted field's values then the field
- `User.GetByEmail(email)` — Find user by email
- `Channel.GetByNameForTeamName(teamName, channelName, includeDeleted)` — Resolve a channel record
- `System.GetLicense()` — Channel field sync checks `MinimumEnterpriseAdvancedLicense` against this
- `User.HasPermissionTo(userID, permission)` — The sysadmin gate on every HTTP route
- `KV.Set/Get/Delete(key, …)` — Storage behind `ReadStoredAttributes` and the upload API. Note `KV.Set` returns `(bool, error)`: a `false` with no error means the write did not happen, and callers here treat that as a failure.
- `Log.Info/Warn/Error/Debug` — Structured logging

Plugin hooks implemented: `OnActivate`, `OnConfigurationChange`, `ServeHTTP`.

## Key Dependencies

**Go:**
- `github.com/mattermost/mattermost/server/public` — Plugin API, model types
- `github.com/gorilla/mux` — HTTP routing behind `ServeHTTP`
- `github.com/pkg/errors` — Error wrapping (note `http_hooks.go` and `kv_store_provider.go` use stdlib `errors`/`fmt.Errorf` with `%w` instead)
- `github.com/stretchr/testify` — Testing

**Node:**
- React 17, Redux, mattermost-redux, @mattermost/types
- `react-bootstrap` — used only by `confirm_modal.tsx`. It is a **webpack external** (`webpack.config.js` maps it to the host's `ReactBootstrap` global, like `react` and `redux`), so it is not installed; only `@types/react-bootstrap` is a devDependency, and it exists purely so `tsc` can resolve the import. Adding the real package would not change the bundle, but see the testing notes above for why it is still not worth doing
- Webpack, Babel, TypeScript, ESLint, Jest
- `e2e/` has its own tree: `@playwright/test`, TypeScript, ESLint + `@typescript-eslint`
