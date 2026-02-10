/**
 * Browser notifications for mentions and direct messages
 */

export interface NotificationOptions {
  body?: string;
  icon?: string;
  tag?: string;
  onClick?: () => void;
}

export class BrowserNotifications {
  private permission: NotificationPermission = 'default';
  private isIdle = false;
  private idleTimer: number | null = null;

  constructor() {
    if ('Notification' in window) {
      this.permission = Notification.permission;
    }
  }

  /**
   * Request notification permission from the user
   * @returns true if permission granted
   */
  async requestPermission(): Promise<boolean> {
    if (!('Notification' in window)) {
      console.warn('Browser notifications not supported');
      return false;
    }

    if (this.permission === 'granted') {
      return true;
    }

    this.permission = await Notification.requestPermission();
    return this.permission === 'granted';
  }

  /**
   * Start tracking user idle state
   * Notifications only show when user is idle or window is not focused
   * @param idleThreshold Time in ms to consider user idle (default: 30s)
   */
  startIdleTracking(idleThreshold = 30000): void {
    const resetIdle = () => {
      this.isIdle = false;
      if (this.idleTimer) {
        window.clearTimeout(this.idleTimer);
      }
      this.idleTimer = window.setTimeout(() => {
        this.isIdle = true;
      }, idleThreshold);
    };

    window.addEventListener('mousemove', resetIdle);
    window.addEventListener('keydown', resetIdle);

    document.addEventListener('visibilitychange', () => {
      this.isIdle = document.hidden;
    });

    resetIdle();
  }

  /**
   * Show a browser notification
   * Only shows if:
   * - Permission is granted
   * - User is idle or window is not focused
   *
   * @param title Notification title
   * @param options Notification options including onClick handler
   */
  notify(title: string, options: NotificationOptions = {}): void {
    if (this.permission !== 'granted') {
      return;
    }

    // Don't show notification if user is active and window is focused
    if (!this.isIdle && document.hasFocus()) {
      return;
    }

    const notification = new Notification(title, {
      icon: options.icon || '/thrum-icon.png',
      body: options.body,
      tag: options.tag,
    });

    if (options.onClick) {
      notification.onclick = () => {
        window.focus();
        options.onClick?.();
        notification.close();
      };
    }

    // Auto-close after 5 seconds
    setTimeout(() => notification.close(), 5000);
  }

  /**
   * Clean up idle tracking event listeners
   */
  cleanup(): void {
    if (this.idleTimer) {
      window.clearTimeout(this.idleTimer);
      this.idleTimer = null;
    }
  }

  /**
   * Get current permission status
   */
  getPermission(): NotificationPermission {
    return this.permission;
  }

  /**
   * Check if user is currently idle
   */
  getIsIdle(): boolean {
    return this.isIdle;
  }
}

// Singleton instance
export const browserNotifications = new BrowserNotifications();
