/* eslint-disable no-process-env */

// The server under test.
//
// MM_SITE_URL is the conventional override across Mattermost's Playwright
// suites, so CI and multi-instance setups can point somewhere else:
//   MM_SITE_URL=http://localhost:8066 npm test
export const baseURL = process.env.MM_SITE_URL || 'http://localhost:8065';

// The system admin these tests log in as.
//
// These defaults are the standard Mattermost development credentials, created by
// `make test-data` in a mattermost-server checkout — they are not specific to any
// one machine. The env var overrides are a convenience for pointing at a server
// with different credentials; they are not a Mattermost-wide convention, so
// nothing outside this repo sets them.
export const adminUsername = process.env.MM_ADMIN_USERNAME || 'sysadmin';
export const adminPassword = process.env.MM_ADMIN_PASSWORD || 'Sys@dmin-sample1';

// Where global-setup.ts saves the admin's authenticated browser state, so specs
// can skip the login screen entirely.
//
// This file contains a live session cookie and must never be committed. If you
// rename it, update `.gitignore` in this directory to match — the two are coupled
// and nothing will warn you if they drift.
export const adminStorageStatePath = 'adminStorageState.json';

// Must match the "id" field in plugin.json. Every URL below is derived from it,
// so if the two disagree the tests will 404 with no obvious explanation.
export const pluginID = 'com.mattermost.user-attribute-sync-test-tool';

// Admin console URL for this plugin's settings section. The System Console
// derives this path from the plugin ID.
export const pluginSettingsURL = `${baseURL}/admin_console/plugins/plugin_${pluginID}`;

// Plugin REST routes, registered in server/http_hooks.go.
export const attributesURL = `${baseURL}/plugins/${pluginID}/attributes`;
export const attributesStatusURL = `${attributesURL}/status`;

// Fixture files.
//
// The valid case is user-only rather than the repository's `data/attributes.json`:
// that file carries channel and graph fields, which create or skip depending on the
// server's license tier and feature flags, and the specs assert exact field counts.
export const validAttributesFile = './assets/attributes.json';
export const legacyArrayFile = './assets/legacy_array.json';
export const wrongVersionFile = './assets/wrong_version.json';
export const malformedFile = './assets/malformed.json';
