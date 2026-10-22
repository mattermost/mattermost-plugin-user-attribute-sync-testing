import {expect, test} from '@playwright/test';

import {adminStorageStatePath} from '../constants';
import PluginSettingsPage from '../pages/plugin_settings_page';

test.describe('user attribute sync settings', () => {
    test.use({storageState: adminStorageStatePath});

    test('renders the upload panel', async ({page}) => {
        const settings = new PluginSettingsPage(page);
        await settings.goto();

        await expect(settings.chooseFileButton()).toBeVisible();
    });
});
