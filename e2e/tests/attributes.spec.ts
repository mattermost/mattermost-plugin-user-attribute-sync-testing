import {expect, test} from '@playwright/test';
import * as fs from 'fs/promises';

import {
    adminStorageStatePath,
    legacyArrayFile,
    malformedFile,
    validAttributesFile,
    wrongVersionFile,
} from '../constants';
import PluginSettingsPage from '../pages/plugin_settings_page';
import {
    adminAPIContext,
    apiDeleteStoredAttributes,
    apiUploadStoredAttributes,
} from '../utils';

// assets/attributes.json ships with three user records.
const validRecordCount = 3;

test.describe('attributes upload and download', () => {
    test.use({storageState: adminStorageStatePath});

    // The stored file survives between tests, so each one starts from a known state.
    test.beforeEach(async () => {
        const admin = await adminAPIContext();
        await apiDeleteStoredAttributes(admin);
        await admin.dispose();
    });

    test('uploads a valid attributes file', async ({page}) => {
        const settings = new PluginSettingsPage(page);
        await settings.goto();

        await settings.expectNoFileOnServer();

        await settings.chooseFile(validAttributesFile);
        await expect(settings.pendingSummary()).toContainText(`${validRecordCount} users`);

        await settings.uploadButton().click();

        await settings.expectFileOnServer();
        await expect(settings.errorText()).toHaveCount(0);
        await expect(settings.successText()).toBeVisible();
        await expect(settings.syncSummary()).toContainText('Fields created: 5');
        await expect(settings.syncSummary()).toContainText('Fields updated: 0');
        await expect(settings.syncSummary()).toContainText('Fields deleted: 0');
        await expect(settings.syncSummary()).toContainText('Fields skipped: 0');
    });

    // Each of these is rejected client-side, before anything is sent, so the assertions below
    // check that the Upload button stays disabled and the server is left untouched.
    const invalidFiles = [
        {name: 'a bare array of user records (the old format)', path: legacyArrayFile},
        {name: 'a document whose version is not supported', path: wrongVersionFile},
        {name: 'text that is not JSON', path: malformedFile},
    ];

    for (const invalid of invalidFiles) {
        test(`rejects ${invalid.name}`, async ({page}) => {
            const settings = new PluginSettingsPage(page);
            await settings.goto();

            await settings.chooseFile(invalid.path);

            await expect(settings.errorText()).toBeVisible();
            await expect(settings.successText()).toHaveCount(0);
            await expect(settings.uploadButton()).toBeDisabled();
            await settings.expectNoFileOnServer();
        });
    }

    test('downloads the stored file', async ({page}) => {
        const admin = await adminAPIContext();
        await apiUploadStoredAttributes(admin, validAttributesFile);
        await admin.dispose();

        const settings = new PluginSettingsPage(page);
        await settings.goto();
        await settings.expectFileOnServer();

        // Start listening before the click: the download can fire before an await
        // placed after it would have subscribed.
        const downloadPromise = page.waitForEvent('download');
        await settings.downloadButton().click();
        const download = await downloadPromise;

        expect(download.suggestedFilename()).toBe('attributes.json');

        const downloadedPath = await download.path();
        const contents: unknown = JSON.parse(await fs.readFile(downloadedPath, 'utf8'));
        const original: unknown = JSON.parse(await fs.readFile(validAttributesFile, 'utf8'));

        expect(contents).toEqual(original);
    });

    test('deletes the stored file and the plugin-owned fields', async ({page}) => {
        const admin = await adminAPIContext();
        await apiUploadStoredAttributes(admin, validAttributesFile);
        await admin.dispose();

        const settings = new PluginSettingsPage(page);
        await settings.goto();
        await settings.expectFileOnServer();

        await settings.deleteWithConfirm();

        await settings.expectNoFileOnServer();
        await expect(settings.downloadButton()).toBeDisabled();
        await expect(settings.deleteButton()).toBeDisabled();
        await expect(settings.successText()).toBeVisible();
        await expect(settings.syncSummary()).toContainText('Fields deleted: 5');
    });

     test('keeps the stored file when the delete modal is dismissed', async ({page}) => {
        const admin = await adminAPIContext();
        await apiUploadStoredAttributes(admin, validAttributesFile);
        await admin.dispose();

        const settings = new PluginSettingsPage(page);
        await settings.goto();
        await settings.expectFileOnServer();

        await settings.deleteButton().click();
        await settings.cancelDeleteButton().click();

        await expect(settings.confirmDeleteDialog()).toBeHidden();
        await settings.expectFileOnServer();
        await expect(settings.downloadButton()).toBeEnabled();
    });
});
