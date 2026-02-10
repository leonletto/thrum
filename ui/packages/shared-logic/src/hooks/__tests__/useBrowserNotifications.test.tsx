import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { useBrowserNotifications, useNotificationState } from '../useBrowserNotifications';
import { browserNotifications } from '../../notifications/browser';
import { wsClient } from '../../api/client';
import type { Message } from '../../types/api';

vi.mock('../../api/client', () => ({
  wsClient: {
    on: vi.fn(),
  },
}));

describe('useBrowserNotifications', () => {
  let mockNotification: any;

  beforeEach(() => {
    // Mock window.Notification
    mockNotification = vi.fn();
    mockNotification.permission = 'granted';
    (global as any).Notification = mockNotification;
    mockNotification.requestPermission = vi.fn().mockResolvedValue('granted');

    // Mock document
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => true,
    });

    vi.spyOn(browserNotifications, 'requestPermission').mockResolvedValue(true);
    vi.spyOn(browserNotifications, 'startIdleTracking');
    vi.spyOn(browserNotifications, 'notify');
    vi.spyOn(browserNotifications, 'cleanup');

    // Reset wsClient mock
    (wsClient.on as any).mockClear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('requests permission and starts idle tracking on mount', async () => {
    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    await waitFor(() => {
      expect(browserNotifications.requestPermission).toHaveBeenCalled();
      expect(browserNotifications.startIdleTracking).toHaveBeenCalled();
    });
  });

  it('does not request permission when disabled', () => {
    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, false)
    );

    expect(browserNotifications.requestPermission).not.toHaveBeenCalled();
  });

  it('cleans up on unmount', async () => {
    const { unmount } = renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    await waitFor(() => {
      expect(browserNotifications.requestPermission).toHaveBeenCalled();
    });

    unmount();

    expect(browserNotifications.cleanup).toHaveBeenCalled();
  });

  it('subscribes to message.created events', () => {
    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    expect(wsClient.on).toHaveBeenCalledWith('message.created', expect.any(Function));
  });

  it('shows notification for mentions', () => {
    const mockOnClick = vi.fn();
    let messageHandler: any;

    (wsClient.on as any).mockImplementation((event: string, handler: any) => {
      if (event === 'message.created') {
        messageHandler = handler;
      }
      return () => {};
    });

    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', mockOnClick, true)
    );

    const message: Message = {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      agent_id: 'agent:bob',
      created_at: new Date().toISOString(),
      body: {
        format: 'markdown',
        content: 'Hey @alice, check this out!',
      },
      scopes: [],
      refs: [],
    };

    messageHandler({ message });

    expect(browserNotifications.notify).toHaveBeenCalledWith(
      'Thrum · TestProject',
      expect.objectContaining({
        body: 'bob: "Hey @alice, check this out!"',
        tag: 'thread-1',
      })
    );
  });

  it('shows notification for direct messages', () => {
    let messageHandler: any;

    (wsClient.on as any).mockImplementation((event: string, handler: any) => {
      if (event === 'message.created') {
        messageHandler = handler;
      }
      return () => {};
    });

    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    const message: Message = {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      session_id: 'session-bob',
      created_at: new Date().toISOString(),
      body: {
        format: 'markdown',
        content: 'Direct message to you',
      },
      scopes: [{ type: 'user', value: 'user:alice' }],
      refs: [],
    };

    messageHandler({ message });

    expect(browserNotifications.notify).toHaveBeenCalledWith(
      'Thrum · TestProject',
      expect.objectContaining({
        body: expect.stringContaining('Direct message to you'),
      })
    );
  });

  it('does not show notification for irrelevant messages', () => {
    let messageHandler: any;

    (wsClient.on as any).mockImplementation((event: string, handler: any) => {
      if (event === 'message.created') {
        messageHandler = handler;
      }
      return () => {};
    });

    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    const message: Message = {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      agent_id: 'agent:bob',
      created_at: new Date().toISOString(),
      body: {
        format: 'markdown',
        content: 'Message to someone else',
      },
      scopes: [{ type: 'user', value: 'user:charlie' }],
      refs: [],
    };

    messageHandler({ message });

    expect(browserNotifications.notify).not.toHaveBeenCalled();
  });

  it('truncates long messages in preview', () => {
    let messageHandler: any;

    (wsClient.on as any).mockImplementation((event: string, handler: any) => {
      if (event === 'message.created') {
        messageHandler = handler;
      }
      return () => {};
    });

    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', undefined, true)
    );

    const longContent = 'a'.repeat(150);
    const message: Message = {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      agent_id: 'agent:bob',
      created_at: new Date().toISOString(),
      body: {
        format: 'markdown',
        content: `@alice ${longContent}`,
      },
      scopes: [],
      refs: [],
    };

    messageHandler({ message });

    expect(browserNotifications.notify).toHaveBeenCalledWith(
      'Thrum · TestProject',
      expect.objectContaining({
        body: expect.stringContaining('...'),
      })
    );

    const callArgs = (browserNotifications.notify as any).mock.calls[0][1];
    expect(callArgs.body.length).toBeLessThan(150);
  });

  it('handles onClick callback', () => {
    const mockOnClick = vi.fn();
    let messageHandler: any;
    let notifyOnClick: any;

    (wsClient.on as any).mockImplementation((event: string, handler: any) => {
      if (event === 'message.created') {
        messageHandler = handler;
      }
      return () => {};
    });

    (browserNotifications.notify as any).mockImplementation(
      (_title: string, options: any) => {
        notifyOnClick = options.onClick;
      }
    );

    renderHook(() =>
      useBrowserNotifications('user:alice', 'TestProject', mockOnClick, true)
    );

    const message: Message = {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      agent_id: 'agent:bob',
      created_at: new Date().toISOString(),
      body: {
        format: 'markdown',
        content: '@alice hello',
      },
      scopes: [],
      refs: [],
    };

    messageHandler({ message });

    expect(notifyOnClick).toBeDefined();
    notifyOnClick();

    expect(mockOnClick).toHaveBeenCalledWith('thread-1');
  });
});

describe('useNotificationState', () => {
  beforeEach(() => {
    vi.spyOn(browserNotifications, 'getPermission').mockReturnValue('default');
    vi.spyOn(browserNotifications, 'getIsIdle').mockReturnValue(false);
    vi.spyOn(browserNotifications, 'requestPermission').mockResolvedValue(true);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns notification state', () => {
    const { result } = renderHook(() => useNotificationState());

    expect(result.current.permission).toBe('default');
    expect(result.current.isIdle).toBe(false);
  });

  it('provides requestPermission function', () => {
    const { result } = renderHook(() => useNotificationState());

    result.current.requestPermission();

    expect(browserNotifications.requestPermission).toHaveBeenCalled();
  });
});
