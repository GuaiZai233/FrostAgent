import { TestBed } from '@angular/core/testing';
import { LogLevel } from '@frostagent/proto';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { FrostagentApiService } from './frostagent-api.service';

vi.mock('@connectrpc/connect', () => ({
  createClient: vi.fn(),
}));

vi.mock('@connectrpc/connect-web', () => ({
  createConnectTransport: vi.fn(() => ({ id: 'transport' })),
}));

describe('FrostagentApiService', () => {
  const botClient = {
    getOverview: vi.fn(),
    getSessions: vi.fn(),
  };
  const logClient = {
    listLogs: vi.fn(),
    streamLogs: vi.fn(),
    clearLogs: vi.fn(),
  };
  const settingsClient = {
    listEnvVars: vi.fn(),
    updateEnvVar: vi.fn(),
    deleteEnvVar: vi.fn(),
    getRawEnvFile: vi.fn(),
    updateRawEnvFile: vi.fn(),
  };

  beforeEach(() => {
    TestBed.resetTestingModule();
    vi.clearAllMocks();
    vi.mocked(createClient)
      .mockReturnValueOnce(botClient as never)
      .mockReturnValueOnce(logClient as never)
      .mockReturnValueOnce(settingsClient as never);
  });

  it('creates a Connect transport against the current origin', async () => {
    botClient.getOverview.mockResolvedValue({ status: 'running' });

    const service = TestBed.inject(FrostagentApiService);
    await service.getOverview();

    expect(createConnectTransport).toHaveBeenCalledWith({
      baseUrl: window.location.origin,
    });
    expect(botClient.getOverview).toHaveBeenCalledWith({});
  });

  it('passes pagination to session requests', async () => {
    botClient.getSessions.mockResolvedValue({ sessions: [] });

    const service = TestBed.inject(FrostagentApiService);
    await service.getSessions(20, '40');

    expect(botClient.getSessions).toHaveBeenCalledWith({
      pagination: {
        pageSize: 20,
        pageToken: '40',
      },
    });
  });

  it('passes filters to log list requests', async () => {
    logClient.listLogs.mockResolvedValue({ entries: [] });

    const service = TestBed.inject(FrostagentApiService);
    await service.listLogs(50, '', LogLevel.WARN, 'SYSTEM');

    expect(logClient.listLogs).toHaveBeenCalledWith({
      pagination: {
        pageSize: 50,
        pageToken: '',
      },
      minLevel: LogLevel.WARN,
      sourceFilter: 'SYSTEM',
    });
  });

  it('passes env var updates through SettingsService', async () => {
    settingsClient.updateEnvVar.mockResolvedValue({ success: true, error: '' });

    const service = TestBed.inject(FrostagentApiService);
    await service.updateEnvVar({
      key: 'MODEL_NAME',
      value: 'gpt-5',
      isSecret: false,
    });

    expect(settingsClient.updateEnvVar).toHaveBeenCalledWith({
      key: 'MODEL_NAME',
      value: 'gpt-5',
      isSecret: false,
    });
  });
});
