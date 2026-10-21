import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import React from 'react';

import UploadUserAttributes from './upload_user_attributes';

let fetchMock: jest.Mock;

const emptySummary = {
    fieldsCreated: 0,
    fieldsUpdated: 0,
    fieldsDeleted: 0,
    fieldsSkipped: 0,
    usersSynced: 0,
    usersSkipped: 0,
    channelsSynced: 0,
    channelsSkipped: 0,
};

function jsonResponse(body: unknown) {
    return {
        ok: true,
        json: () => Promise.resolve(body),
    };
}

function requestMethod(init?: RequestInit): string {
    return (init?.method ?? 'GET').toUpperCase();
}

beforeEach(() => {
    // The panel asks the server what is stored as soon as it mounts, and jsdom provides no fetch.
    // Answering "no file" keeps that out of the way of tests that do not care about storage.
    fetchMock = jest.fn().mockResolvedValue(jsonResponse({exists: false, lastUpdated: null}));
    globalThis.fetch = fetchMock as unknown as typeof fetch;
});

// Renders the panel and lets its mount-time status request settle, so the state update it triggers
// happens inside act() rather than after the test has finished.
const renderPanel = async (props: Partial<React.ComponentProps<typeof UploadUserAttributes>> = {}) => {
    const result = render(
        <UploadUserAttributes
            id='Attributes'
            {...props}
        />,
    );

    await act(async () => {
        await Promise.resolve();
    });

    await waitFor(() => {
        expect(screen.queryByText('Unknown')).not.toBeInTheDocument();
    });

    return result;
};

test('renders the upload controls', async () => {
    await renderPanel();

    expect(screen.getByRole('button', {name: 'Choose File'})).toBeInTheDocument();
});

test('disables the upload controls when the plugin itself is disabled', async () => {
    await renderPanel({disabled: true});

    expect(screen.getByRole('button', {name: 'Choose File'})).toBeDisabled();
});

async function chooseAttributesFile(users: unknown[] = [{email: 'user@example.com'}]) {
    const contents = JSON.stringify({version: 2, users});
    const file = new File(
        [contents],
        'attributes.json',
        {type: 'application/json'},
    );

    // jsdom's File does not implement blob.text(), which handleFileChange uses.
    Object.defineProperty(file, 'text', {
        value: () => Promise.resolve(contents),
    });
    const input = screen.getByTestId('userAttrSyncFileInput');

    await act(async () => {
        fireEvent.change(input, {target: {files: [file]}});
    });

    await waitFor(() => {
        expect(screen.getByRole('button', {name: 'Upload'})).toBeEnabled();
    });
}

test('shows the sync summary after upload and refreshes file status from the status endpoint', async () => {
    let stored = false;
    fetchMock.mockImplementation((url: RequestInfo, init?: RequestInit) => {
        const method = requestMethod(init);
        if (method === 'POST') {
            stored = true;
            return Promise.resolve(jsonResponse({
                ...emptySummary,
                fieldsCreated: 2,
                usersSynced: 1,
            }));
        }
        if (String(url).includes('/attributes/status')) {
            return Promise.resolve(jsonResponse(
                stored ?
                    {exists: true, lastUpdated: '2026-09-15T12:00:00.000Z'} :
                    {exists: false, lastUpdated: null},
            ));
        }
        return Promise.resolve(jsonResponse({exists: false, lastUpdated: null}));
    });

    await renderPanel();
    expect(screen.getByText('No file on server')).toBeInTheDocument();

    await chooseAttributesFile();
    await act(async () => {
        fireEvent.click(screen.getByRole('button', {name: 'Upload'}));
    });

    expect(await screen.findByText('File uploaded')).toBeInTheDocument();
    expect(screen.getByText('Fields created: 2')).toBeInTheDocument();
    expect(screen.getByText('Fields updated: 0')).toBeInTheDocument();
    expect(screen.getByText('Fields deleted: 0')).toBeInTheDocument();
    expect(screen.getByText('Fields skipped: 0')).toBeInTheDocument();
    expect(screen.getByText('Users synced: 1')).toBeInTheDocument();
    expect(screen.getByText('Users skipped: 0')).toBeInTheDocument();
    expect(screen.getByText('Channels synced: 0')).toBeInTheDocument();
    expect(screen.getByText('Channels skipped: 0')).toBeInTheDocument();
    expect(screen.getByText(/File detected/)).toBeInTheDocument();
});

test('enables download when upload stores the file but sync fails', async () => {
    let stored = false;
    fetchMock.mockImplementation((url: RequestInfo, init?: RequestInit) => {
        if (requestMethod(init) === 'POST') {
            stored = true;
            return Promise.resolve({
                ok: false,
                status: 500,
                json: () => Promise.resolve({error: 'document stored but sync failed: boom'}),
            });
        }
        if (String(url).includes('/attributes/status')) {
            return Promise.resolve(jsonResponse(
                stored ?
                    {exists: true, lastUpdated: '2026-09-15T12:00:00.000Z'} :
                    {exists: false, lastUpdated: null},
            ));
        }
        return Promise.resolve(jsonResponse({exists: false, lastUpdated: null}));
    });

    await renderPanel();
    await chooseAttributesFile();
    await act(async () => {
        fireEvent.click(screen.getByRole('button', {name: 'Upload'}));
    });

    expect(await screen.findByText('document stored but sync failed: boom')).toBeInTheDocument();
    expect(screen.getByRole('button', {name: 'Download'})).toBeEnabled();
});

test('shows a zero sync summary after upload', async () => {
    let stored = false;
    fetchMock.mockImplementation((url: RequestInfo, init?: RequestInit) => {
        if (requestMethod(init) === 'POST') {
            stored = true;
            return Promise.resolve(jsonResponse(emptySummary));
        }
        if (String(url).includes('/attributes/status')) {
            return Promise.resolve(jsonResponse(
                stored ?
                    {exists: true, lastUpdated: '2026-09-15T12:00:00.000Z'} :
                    {exists: false, lastUpdated: null},
            ));
        }
        return Promise.resolve(jsonResponse({exists: false, lastUpdated: null}));
    });

    await renderPanel();
    await chooseAttributesFile();
    await act(async () => {
        fireEvent.click(screen.getByRole('button', {name: 'Upload'}));
    });

    expect(await screen.findByText('Fields created: 0')).toBeInTheDocument();
    expect(screen.getByText('Users synced: 0')).toBeInTheDocument();
    expect(screen.getByText('Channels synced: 0')).toBeInTheDocument();
    expect(document.querySelector('.error-text')).not.toBeInTheDocument();
});

test('shows the sync summary after delete and states that attributes are removed', async () => {
    fetchMock.mockImplementation((_url: RequestInfo, init?: RequestInit) => {
        if (requestMethod(init) === 'DELETE') {
            return Promise.resolve(jsonResponse({
                ...emptySummary,
                fieldsDeleted: 4,
            }));
        }
        return Promise.resolve(jsonResponse({exists: true, lastUpdated: '2026-09-15T12:00:00.000Z'}));
    });

    await renderPanel();
    fireEvent.click(screen.getByRole('button', {name: 'Delete'}));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Delete stored attributes document?');
    expect(dialog).toHaveTextContent('every attribute this plugin created is deleted');
    expect(dialog).not.toHaveTextContent('will remain');

    await act(async () => {
        fireEvent.click(within(dialog).getByRole('button', {name: 'Delete'}));
    });

    expect(await screen.findByText('File Deleted')).toBeInTheDocument();
    expect(screen.getByText('Fields deleted: 4')).toBeInTheDocument();
    expect(screen.getByText('Channels synced: 0')).toBeInTheDocument();
    expect(screen.getByText('No file on server')).toBeInTheDocument();
});
