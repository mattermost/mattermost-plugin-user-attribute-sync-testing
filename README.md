# User Attribute Sync Test Tool

A Mattermost plugin that creates plugin-managed user and channel attributes and fills them in from a JSON file. Use it to set up test environments that need attributes the System Console cannot create.

Attributes created by a plugin belong to that plugin. They are marked `protected`, which means only the plugin can change their definition or write their values — admins cannot edit them, through the System Console or the REST API. The `source_only` and `shared_only` access modes are available only on attributes like these.

Field definitions and values both come from a JSON file uploaded through the System Console. The attributes live in the `access_control` property group, so ABAC policies can reference user attributes as `user.attributes.<name>` and channel attributes as `resource.attributes.<name>`.

This repository is a fork of [mattermost-plugin-user-attribute-sync-starter-template](https://github.com/mattermost/mattermost-plugin-user-attribute-sync-starter-template). If you want a minimal starting point for your own plugin, use that one instead.

## What it creates

The example file `data/attributes.json` defines a template, five user attributes, and two channel attributes, so one upload exercises every field type the plugin supports. Edit that file (or upload a different document) to change the set.

| Attribute | Type | Access mode | What it exercises |
|---|---|---|---|
| `job_title` | text | public | Plugin-managed but readable by everyone |
| `programs` | multiselect | `shared_only` | Viewers see only the options they share with the target user |
| `clearance` | rank | `shared_only` | Ordered levels, so policies can say `is at least` — declared as a template the user attribute links to |
| `start_date` | date | `source_only` | No one reads it through the API, not even the user themselves |
| `org_unit` | graph | public | A hierarchy of options; needs the `PropertyFieldGraph` feature flag |
| `clearance` (channel) | rank | `shared_only` | Linked to the same template as the user attribute, so a policy can compare `user.attributes.clearance` with `resource.attributes.clearance` |
| `region` (channel) | select | public | A single-value channel attribute |

`clearance` is defined once in `fields.template` and the user and channel attributes carry only a `template` key naming it — see [Link attributes to a template](#link-attributes-to-a-template).

Every attribute this plugin creates is `protected` (not settable in the document — always `true`), which is what makes `source_only` and `shared_only` legal. The example fields use `visibility: always`. `job_title` is public, so everyone can read it — it is still plugin-managed, and still not editable by an admin.

On a server without an Enterprise Advanced license the two channel attributes are skipped, and with the `PropertyFieldGraph` feature flag off `org_unit` is refused; either shows up in the upload summary's skipped-fields count, and the rest of the file still syncs.

The attributes appear in **System Console → User Attributes**, on user profiles, and in the ABAC policy editor.

## Requirements

- Mattermost server 12.0.0 or later (the `graph` attribute type requires it, and a server whose `PropertyFieldGraph` feature flag is off refuses graph fields)
- An Enterprise Advanced license, for ABAC. Channel attributes need this tier specifically: below it every channel field is skipped and counted in the upload summary's skipped-fields number, and user attributes still sync.
- Go 1.26.7 or later, to build the server binaries
- Node v20.11, to build the webapp bundle and run the end-to-end tests

## Install

1. Build and deploy the plugin:

   ```bash
   make deploy
   ```

   Or run `make` and upload `dist/*.tar.gz` through **System Console → Plugin Management**.

2. Edit `data/attributes.json` to use the email addresses of users on your server, then go to **System Console → Plugins → User Attribute Sync Test Tool** and upload it with **Choose File** and **Upload**. The upload applies the document immediately — fields and values — and the panel shows what happened.

With nothing uploaded, activation does not create or delete attributes — a first install, or a restart after a reset, must not tear down fields nobody asked to remove. A redeploy with a document already stored reapplies it; there is nothing to configure and no need to disable the plugin.

## The data file

A JSON document with a `version`, a `fields.user` list, a `users` array, and optionally `fields.template`, `fields.channel`, and `channels`:

```json
{
  "version": 2,
  "fields": {
    "user": [
      {
        "name": "job_title",
        "display_name": "Job Title",
        "type": "text",
        "access_mode": "public"
      }
    ]
  },
  "users": [
    {
      "email": "john.doe@example.com",
      "job_title": "Software Engineer"
    }
  ]
}
```

`data/attributes.json` in this repository is an example of the format — the attributes in **What it creates** live there, not in Go. Replace the email addresses and team name with your own before uploading it. `version` must be `2` — an older upload in the bare-array format is rejected.

The document can also define channel attributes: a `fields.channel` list in the same shape as `fields.user`, and a `channels` array whose records are identified by team name and channel name rather than email:

```json
{
  "fields": {
    "channel": [
      {
        "name": "classification",
        "display_name": "Classification",
        "type": "multiselect",
        "access_mode": "shared_only",
        "options": [{"name": "Alpha-1"}, {"name": "Beta-2"}]
      }
    ]
  },
  "channels": [
    {
      "team": "ad-1",
      "channel": "town-square",
      "classification": ["Alpha-1"]
    }
  ]
}
```

Channel fields are `ObjectType=channel` fields in the same `access_control` group, addressable from ABAC as `resource.attributes.<name>` where a user attribute is `user.attributes.<name>`. `team` and `channel` are identity keys — they match the record to a channel and are never written as attribute values. A team/channel pair that resolves to no channel is skipped with a warning, the same as an email matching no user; the rest of the document still syncs.

The uploaded document is the full list of fields this plugin owns. A field it previously created that the document omits is deleted along with its values when the document is applied. This is per object type: a document that defines `fields.user` but no `fields.channel` deletes the plugin's channel fields, and the reverse. Fields owned by an admin or another plugin are left alone. It is not the full list of values: a user or channel the document does not mention keeps whatever it already has. Values disappear only when their field does.

`visibility`, `access_mode`, `permission_field`, `permission_values`, and `permission_options` are optional. If omitted they default to visibility `always`, access mode `public`, and permission levels `sysadmin`. Accepted values:

- visibility: `always` / `hidden` / `when_set`
- access mode: `public` / `source_only` / `shared_only`
- permission levels: `none` / `sysadmin` / `member` / `admin`

`shared_only` with `permission_values: member` is a combination the server rejects. A rejected field is skipped and logged; the rest of the document still syncs. `protected` is not a document field — it is always `true`.

Display actions are not a document field either. Every field the plugin creates or updates gets all three — the top banner, the channel header label, and the channel info label — so a value shows up in each of those places wherever the UI supports it for that object type. There is currently no way to choose a subset per field.

- `email` matches a user record to a Mattermost user; `team` and `channel` match a channel record. None of them are written as attributes.
- Every other key on a record is an attribute `name` from `fields.user` (user records) or `fields.channel` (channel records). Keys that do not match a field in that list are skipped with a warning in the logs.
- Text and date values are strings; dates use `YYYY-MM-DD`.
- Multiselect values are an array of option names. Rank and select values are a single option name. Option names are translated to the option IDs Mattermost generated when it created the attribute, so they have to match the field's `options` exactly.
- Records that match no user or channel are skipped, as are individual values that fail to convert. The rest of the file still syncs.

## Where the values come from

The plugin reads the attributes file from its key-value store, populated by uploading it in the System Console.

### Managing the uploaded file

The settings section shows a panel for the stored file. It reports whether a file is present and when it was uploaded, and lets you replace, download, or delete it. Downloading returns the exact bytes that were uploaded, which is the quickest way to confirm what the plugin is working from. After an upload or a delete, the panel lists how many fields were created, updated, deleted, and skipped, and how many users and channels were synced or skipped. Deleting asks for confirmation: it removes the stored document and every attribute this plugin created, along with the values on user profiles.

These buttons act immediately and do not go through the console's **Save** button.

Files are checked before upload: they must be a JSON document with `version: 2` and no larger than 10 MB. Individual records are not validated, because one bad record should not stop the rest of the file from syncing.

### Loading data over HTTP

The upload panel is a client for four endpoints, which are useful for seeding a test server from a script. All of them require a system admin.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/attributes` | Store a document and sync it. The body is the raw JSON. `201` answers with the sync summary (`fieldsCreated`, `fieldsUpdated`, `fieldsDeleted`, `fieldsSkipped`, `usersSynced`, `usersSkipped`, `channelsSynced`, `channelsSkipped`). If the document is stored but the sync fails, `500`. |
| `GET` | `/attributes` | Download the stored document. |
| `GET` | `/attributes/status` | `{"exists": bool, "lastUpdated": time\|null}` |
| `DELETE` | `/attributes` | Remove the stored document and every field this plugin owns (values first). `200` answers with the same summary shape; field-delete counts are filled in, the rest stay `0`. If the document is removed but the plugin cannot list its fields, the answer is `500` and the fields stay — delete again to retry. |

Full paths are prefixed with `/plugins/com.mattermost.user-attribute-sync-test-tool`.

```bash
curl -X POST \
  -H "Authorization: Bearer $MM_ADMIN_TOKEN" \
  --data-binary @data/attributes.json \
  http://localhost:8065/plugins/com.mattermost.user-attribute-sync-test-tool/attributes
```

## How syncing works

An upload stores the document and then applies it: fields from `fields.user` and `fields.channel`, then per-user and per-channel values. The HTTP response is the summary the System Console panel shows, so you do not have to read the plugin log to see what happened. Two uploads (or an upload and an activation) cannot interleave — they wait on one lock.

If a document is already stored, activation reapplies it the same way. With no document stored, activation does nothing — it cannot delete fields.

`DELETE /attributes` is a reset, not just a file removal: the stored document goes first, then every field this plugin owns (and their values). A restart after that leaves attributes alone, because nothing is stored. The confirmation modal in the console gates it.

Per-user and per-field problems still go to the server log:

```bash
make logs-watch
```

## Access modes

The access mode controls who can read an attribute's values through the API and the UI.

**Public** (`job_title`) — everyone can read every value. This is the default when no access mode is set.

**Shared only** (`programs`, `clearance`) — a user sees only the options and values they have in common with the user they are looking at. If Alice is in [Apples, Oranges] and Bob is in [Oranges, Lemons], Alice sees only Oranges on Bob's profile. On a rank attribute, a user sees their own level and lower. Works with select, multiselect, and rank only.

**Source only** (`start_date`) — only this plugin can read the values. Everyone else, including admins, integrations, and the user themselves, sees no value and no options.

`source_only` and `shared_only` require the attribute to be `protected`, which is why they are only available on plugin-managed attributes. These are not LDAP or SAML synced attributes, which are locked to those sync jobs instead.

Access mode is separate from `visibility`, which only controls whether values are shown in the Mattermost UI. Hiding an attribute in the UI does not restrict API access to it.

## Changing which attributes it creates

Attribute definitions live in `fields.user` (and `fields.channel` for channel attributes) in the uploaded document. Edit `data/attributes.json` (or whatever you upload), then upload it.

### Add an attribute

```json
{
  "name": "department",
  "display_name": "Department",
  "type": "text",
  "access_mode": "public"
}
```

`name` is the identifier used as the key on user records and as `user.attributes.<name>` in ABAC policies, so it must be a valid CEL identifier — no spaces or punctuation. `display_name` is the label shown in the UI.

### Change select or multiselect options

```json
{
  "name": "programs",
  "display_name": "Programs",
  "type": "multiselect",
  "access_mode": "shared_only",
  "options": [
    {"name": "Apples"},
    {"name": "Oranges"},
    {"name": "Lemons"},
    {"name": "Bananas"}
  ]
}
```

Mattermost generates an ID for each option and stores values as those IDs. The plugin reads the IDs back and translates names from the data file when it writes values. An option dropped from a field's `options` list is removed from the field when the document is applied, and any stored value pointing at it is left pointing at an option ID that no longer exists — silently, with nothing logged.

### Add a rank attribute

A rank attribute works like a select — a user holds one option — except each option carries an integer that defines the ordering:

```json
{
  "name": "clearance",
  "display_name": "Clearance",
  "type": "rank",
  "access_mode": "shared_only",
  "options": [
    {"name": "Unclassified", "rank": 1},
    {"name": "CUI", "rank": 2},
    {"name": "Confidential", "rank": 3},
    {"name": "Secret", "rank": 4},
    {"name": "Top Secret", "rank": 5}
  ]
}
```

The ordering is what lets a policy express a threshold with `is at least` instead of listing every qualifying option, so `user.attributes.clearance >= "Secret"` matches both Secret and Top Secret. Every option on a rank attribute needs a `rank`, and the plugin refuses to create or update the attribute otherwise.

### Add a graph attribute

A graph attribute is a multi-value select whose options form a hierarchy: an object holds several options at once, like a multiselect, and there is no single-value variant. Each option may name the options directly above it in a `parents` list, by name, and those names resolve within the same `options` list — so one upload builds the whole hierarchy:

```json
{
  "name": "classification",
  "display_name": "Classification",
  "type": "graph",
  "access_mode": "public",
  "options": [
    {"name": "Alpha"},
    {"name": "Alpha-1", "parents": ["Alpha"]},
    {"name": "Alpha-2", "parents": ["Alpha"]}
  ]
}
```

A value for it is a list of option names, exactly like a multiselect: `{"email": "a@b.com", "classification": ["Alpha-1"]}`.

How graph fields behave:

- The server's `PropertyFieldGraph` feature flag must be on; a server with it off refuses the field.
- Option names must be unique within the field — parents are referenced by name, so a name has to identify one option.
- A graph option cannot carry a `rank`.
- A parent must exist in the same `options` list, can be named only once per option, and cannot be the option itself.
- An option that omits `parents` keeps whatever parents it already has — the upload says nothing about them rather than clearing them. Detaching an option means writing `"parents": []`.
- An update whose `options` list drops an option that still has an option below it is refused; remove or re-parent the lower option in the same upload.

A field the server refuses is skipped and logged, and the rest of the document still syncs. Options are written inline in the same field write as the definition, so the practical ceiling is the upload size limit — enough for a hand-authored test hierarchy, not the server's 100,000-option scale.

### Link attributes to a template

A template is a field definition in a `fields.template` list that user and channel attributes link to by carrying a `template` key naming it. Linked attributes serve the template's option set, so a user attribute and a channel attribute linked to one template share option IDs — which is what lets an ABAC policy compare `user.attributes.classification` against `resource.attributes.classification`:

```json
{
  "fields": {
    "template": [
      {
        "name": "classification",
        "display_name": "Classification",
        "type": "multiselect",
        "access_mode": "shared_only",
        "options": [{"name": "Alpha-1"}, {"name": "Beta-2"}]
      }
    ],
    "channel": [
      {
        "name": "classification",
        "display_name": "Classification",
        "template": "classification"
      }
    ],
    "user": [
      {
        "name": "classification",
        "display_name": "Classification",
        "template": "classification"
      }
    ]
  }
}
```

How linked fields behave:

- On a definition carrying `template`, `type`, `options`, and `access_mode` are ignored — the server copies all three from the template. `visibility` and the `permission_*` keys still apply as declared.
- The template and the fields linking to it can go in one upload: templates sync before the fields that link to them.
- Name uniqueness is per object type, so the template, the user attribute, and the channel attribute can all be named `classification`.
- Templates hold no values. A template name used as a key on a user or channel record is an unknown field, skipped with a warning like any other unknown key.
- `fields.template` is the full list of templates the plugin owns, like `fields.user` and `fields.channel` are for theirs: an omitted template is deleted. Linked fields are deleted first, because the server refuses to delete a template that still has fields linked to it.
- A `template` key naming a template the document does not define fails that one field — logged, counted in the summary's skipped-fields number, and the rest of the document still syncs. If that leaves a linked field on the server blocking its template's deletion, that failure is logged too and both fields stay; correct the document or reset the plugin.
- The Enterprise Advanced gate on channel attributes applies to linked ones too, but templates are created regardless of tier: below that tier a document with templates produces the templates and the linked user attributes, and its channel attributes are skipped.

### Attribute types cannot change

Mattermost does not allow an attribute's type to change after it is created, and conversions to or from `graph` are refused outright on top of that. To change one, upload a document that omits the field — which deletes it and its values — then upload again with the new type.

## Troubleshooting

**Nothing synced.** Check the log with `make logs-watch`. The usual causes are no file uploaded, or emails in the file matching no user on the server. A sync that matches no users logs warnings and still finishes successfully.

**The attributes are read-only in the System Console.** Expected. Every attribute this plugin creates is `protected`, so only the plugin can change its definition or values.

**The plugin fails to activate.** Attribute operations on the `access_control` group need a license. Check that the server has one.

## Limitations

- Users are matched by email address only.
- When a file changes, every record in it is synced again. There is no per-record diffing.
- A field's link state cannot be changed in place. Uploading a document that links a previously unlinked field, or unlinks a linked one, makes the plugin delete and recreate the field — its values are lost, and an ABAC policy referencing it breaks until the policy is re-pointed. A field of the same name not owned by this plugin is left alone and counted as skipped.

## Development

```text
.
├── server/
│   ├── sync/
│   │   ├── field_sync.go         # Schema reconciliation from the uploaded document
│   │   ├── value_sync.go         # Writing per-user and per-channel values
│   │   ├── sync.go               # SyncDocument: fields then values, returning the summary
│   │   ├── document.go           # Parses the uploaded document, shared by upload and sync
│   │   └── kv_store_provider.go  # Stored document in the key-value store
│   ├── plugin.go                 # OnActivate
│   ├── configuration.go          # Empty plugin configuration
│   ├── http_hooks.go             # The /attributes endpoints
│   └── job.go                    # runSync: read stored document, SyncDocument, under a lock
├── webapp/src/
│   ├── index.tsx                 # Registers the custom admin console setting
│   └── components/
│       ├── upload_user_attributes.tsx  # Upload / download / delete panel
│       └── confirm_modal.tsx           # Confirmation dialog for deletion
├── e2e/                          # Playwright tests — see e2e/README.md
└── data/
    └── attributes.json           # Example data file
```

```bash
make                  # check-style, test, and build
make test             # Go and webapp unit tests
make check-style      # Linting and type checking
make deploy           # Build and deploy to a running server
make watch            # Rebuild the webapp bundle when its sources change
make deploy-from-watch  # Install the bundle that `make watch` rebuilt
make logs-watch       # Tail the plugin's log on the running server
```

The end-to-end tests need a running server with the plugin deployed, so they are separate from `make test`:

```bash
make test-e2e      # Playwright, against http://localhost:8065 by default
```

See [`e2e/README.md`](e2e/README.md) for prerequisites, how to run a single spec, and the conventions those tests follow.

`make deploy` connects over the server's local mode socket when one is available, which needs local mode enabled in the server's configuration:

```json
{
    "ServiceSettings": {
        "EnableLocalMode": true,
        "LocalModeSocketLocation": "/var/tmp/mattermost_local.socket"
    }
}
```

Without a socket it falls back to the REST API, using `MM_SERVICESETTINGS_SITEURL` with either `MM_ADMIN_TOKEN` or `MM_ADMIN_USERNAME` and `MM_ADMIN_PASSWORD`.

## License

See [LICENSE](LICENSE).
