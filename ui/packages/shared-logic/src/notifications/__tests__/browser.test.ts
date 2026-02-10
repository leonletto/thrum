import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { BrowserNotifications } from '../browser';

describe('BrowserNotifications', () => {
  let notifications: BrowserNotifications;
  let mockNotification: any;

  beforeEach(() => {
    // Mock window.Notification
    mockNotification = vi.fn();
    mockNotification.permission = 'default';
    (global as any).Notification = mockNotification;
    mockNotification.requestPermission = vi.fn().mockResolvedValue('granted');

    // Mock document
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    });

    Object.defineProperty(document, 'hasFocus', {
      configurable: true,
      value: () => true,
    });

    notifications = new BrowserNotifications();
  });

  afterEach(() => {
    vi.clearAllMocks();
    notifications.cleanup();
  });

  describe('requestPermission', () => {
    it('requests notification permission', async () => {
      const granted = await notifications.requestPermission();

      expect(granted).toBe(true);
      expect(mockNotification.requestPermission).toHaveBeenCalled();
    });

    it('returns false if notifications not supported', async () => {
      delete (global as any).Notification;

      const granted = await notifications.requestPermission();

      expect(granted).toBe(false);
    });

    it('returns true if already granted', async () => {
      mockNotification.permission = 'granted';
      notifications = new BrowserNotifications();

      const granted = await notifications.requestPermission();

      expect(granted).toBe(true);
      expect(mockNotification.requestPermission).not.toHaveBeenCalled();
    });
  });

  describe('startIdleTracking', () => {
    it('sets up event listeners', () => {
      const addEventListenerSpy = vi.spyOn(window, 'addEventListener');

      notifications.startIdleTracking(1000);

      expect(addEventListenerSpy).toHaveBeenCalledWith('mousemove', expect.any(Function));
      expect(addEventListenerSpy).toHaveBeenCalledWith('keydown', expect.any(Function));
    });

    it('marks user as idle after threshold', () => {
      vi.useFakeTimers();

      notifications.startIdleTracking(1000);
      expect(notifications.getIsIdle()).toBe(false);

      vi.advanceTimersByTime(1000);
      expect(notifications.getIsIdle()).toBe(true);

      vi.useRealTimers();
    });

    it('resets idle timer on mouse move', () => {
      vi.useFakeTimers();

      notifications.startIdleTracking(1000);

      vi.advanceTimersByTime(500);
      window.dispatchEvent(new Event('mousemove'));

      vi.advanceTimersByTime(500);
      expect(notifications.getIsIdle()).toBe(false);

      vi.advanceTimersByTime(500);
      expect(notifications.getIsIdle()).toBe(true);

      vi.useRealTimers();
    });

    it('marks user as idle when page hidden', () => {
      Object.defineProperty(document, 'hidden', {
        configurable: true,
        get: () => true,
      });

      notifications.startIdleTracking();
      document.dispatchEvent(new Event('visibilitychange'));

      expect(notifications.getIsIdle()).toBe(true);
    });
  });

  describe('notify', () => {
    beforeEach(async () => {
      await notifications.requestPermission();
    });

    it('shows notification when user is idle', () => {
      vi.useFakeTimers();

      notifications.startIdleTracking(100);
      vi.advanceTimersByTime(100);

      notifications.notify('Test Title', { body: 'Test body' });

      expect(mockNotification).toHaveBeenCalledWith('Test Title', {
        icon: '/thrum-icon.png',
        body: 'Test body',
        tag: undefined,
      });

      vi.useRealTimers();
    });

    it('shows notification when page is hidden', () => {
      Object.defineProperty(document, 'hidden', {
        configurable: true,
        get: () => true,
      });

      notifications.startIdleTracking();
      document.dispatchEvent(new Event('visibilitychange'));

      notifications.notify('Test Title', { body: 'Test body' });

      expect(mockNotification).toHaveBeenCalled();
    });

    it('does not show notification when user is active and focused', () => {
      notifications.startIdleTracking();

      notifications.notify('Test Title', { body: 'Test body' });

      expect(mockNotification).not.toHaveBeenCalled();
    });

    it('does not show notification without permission', () => {
      mockNotification.permission = 'denied';
      notifications = new BrowserNotifications();

      notifications.notify('Test Title', { body: 'Test body' });

      expect(mockNotification).not.toHaveBeenCalled();
    });

    it('handles onClick callback', () => {
      vi.useFakeTimers();

      const onClick = vi.fn();
      const mockNotificationInstance = {
        close: vi.fn(),
        onclick: null as any,
      };

      mockNotification.mockImplementation(() => mockNotificationInstance);

      notifications.startIdleTracking(100);
      vi.advanceTimersByTime(100);

      notifications.notify('Test Title', { onClick });

      expect(mockNotificationInstance.onclick).toBeDefined();

      // Trigger click
      mockNotificationInstance.onclick();

      expect(onClick).toHaveBeenCalled();
      expect(mockNotificationInstance.close).toHaveBeenCalled();

      vi.useRealTimers();
    });

    it('uses custom icon', () => {
      vi.useFakeTimers();

      notifications.startIdleTracking(100);
      vi.advanceTimersByTime(100);

      notifications.notify('Test Title', { icon: '/custom-icon.png' });

      expect(mockNotification).toHaveBeenCalledWith('Test Title', {
        icon: '/custom-icon.png',
        body: undefined,
        tag: undefined,
      });

      vi.useRealTimers();
    });
  });

  describe('cleanup', () => {
    it('clears idle timer', () => {
      vi.useFakeTimers();

      notifications.startIdleTracking(1000);
      notifications.cleanup();

      vi.advanceTimersByTime(1000);
      expect(notifications.getIsIdle()).toBe(false);

      vi.useRealTimers();
    });
  });

  describe('getPermission', () => {
    it('returns current permission state', async () => {
      expect(notifications.getPermission()).toBe('default');

      await notifications.requestPermission();

      expect(notifications.getPermission()).toBe('granted');
    });
  });
});
