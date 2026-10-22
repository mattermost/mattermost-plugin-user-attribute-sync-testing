import {expect, Page} from '@playwright/test';

import {pluginID, pluginSettingsURL} from '../constants';

/**
 * Page object for this plugin's System Console settings section.
 *
 * Another part of the UI — a channel header menu, a right-hand sidebar — gets a
 * sibling file here rather than an extension of this one.
 */
export default class PluginSettingsPage {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    async goto() {
        await this.page.goto(pluginSettingsURL);
        await expect(this.page.getByTestId('plugin-metadata-id')).toHaveText(pluginID);
    }

    // --- locators ----------------------------------------------------------

    chooseFileButton() {
        return this.page.getByRole('button', {name: 'Choose File'});
    }

    uploadButton() {
        return this.page.getByRole('button', {name: 'Upload'});
    }

    downloadButton() {
        return this.page.getByRole('button', {name: 'Download'});
    }

    // Scoped to the settings panel because the confirmation dialog has a Delete button too, and an
    // unscoped lookup would match both once the dialog is open.
    deleteButton() {
        return this.page.locator('.UserAttrSync').getByRole('button', {name: 'Delete'});
    }

    // Located by its accessible name, which comes from the aria-labelledby heading in
    // confirm_modal.tsx. Changing that title text means changing it here.
    confirmDeleteDialog() {
        return this.page.getByRole('dialog', {name: 'Delete stored attributes document?'});
    }

    confirmDeleteButton() {
        return this.confirmDeleteDialog().getByRole('button', {name: 'Delete'});
    }

    cancelDeleteButton() {
        return this.confirmDeleteDialog().getByRole('button', {name: 'Cancel'});
    }

    saveButton() {
        return this.page.getByRole('button', {name: 'Save'});
    }

    // The file input is display:none, so it has no accessible role to locate it
    // by. That is why it carries a data-testid where nothing else here does.
    fileInput() {
        return this.page.getByTestId('userAttrSyncFileInput');
    }

    /** The "N users - N KB" line shown after picking a valid file. */
    pendingSummary() {
        return this.page.getByText(/users/);
    }

    errorText() {
        return this.page.locator('.error-text');
    }

    successText() {
        return this.page.locator('.success-text');
    }

    syncSummary() {
        return this.page.locator('.UserAttrSync').getByRole('list');
    }

    // --- actions -----------------------------------------------------------

    async save() {
        await this.saveButton().click();
    }

    async chooseFile(filePath: string) {
        await this.fileInput().setInputFiles(filePath);
    }

    async chooseAndUpload(filePath: string) {
        await this.chooseFile(filePath);
        await this.uploadButton().click();
    }

    async deleteWithConfirm() {
        await this.deleteButton().click();
        await this.confirmDeleteButton().click();
    }

    // --- assertions --------------------------------------------------------

    async expectFileOnServer() {
        await expect(this.page.getByText('File detected')).toBeVisible();
    }

    async expectNoFileOnServer() {
        await expect(this.page.getByText('No file on server')).toBeVisible();
    }
}
