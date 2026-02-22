import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@/test/test-utils';
import { SettingsView } from '../SettingsView';
import * as sharedLogic from '@thrum/shared-logic';

vi.mock('@thrum/shared-logic', () => ({
  useHealth: vi.fn(),
  useNotificationState: vi.fn(),
  useTheme: vi.fn(),
}));

const healthOk = {
  data: {
    status: 'ok',
    version: '1.2.3',
    uptime_ms: 9252000,
    repo_id: 'abc123def456',
    sync_state: 'synced',
  },
  isLoading: false,
  error: null,
};

function setupDefaultMocks() {
  vi.mocked(sharedLogic.useHealth).mockReturnValue(healthOk as any);
  vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
    permission: 'default',
    isIdle: false,
    requestPermission: vi.fn().mockResolvedValue(true),
  });
  vi.mocked(sharedLogic.useTheme).mockReturnValue({
    theme: 'system',
    setTheme: vi.fn(),
  });
}

describe('SettingsView', () => {
  beforeEach(() => {
    setupDefaultMocks();
  });

  // ------------------------------------------------------------------
  // Daemon Status section
  // ------------------------------------------------------------------

  describe('Daemon Status section', () => {
    it('renders the Daemon Status heading', () => {
      render(<SettingsView />);
      expect(screen.getByText('Daemon Status')).toBeInTheDocument();
    });

    it('shows health data when loaded', () => {
      render(<SettingsView />);
      expect(screen.getByText('ok')).toBeInTheDocument();
      expect(screen.getByText('1.2.3')).toBeInTheDocument();
      expect(screen.getByText('synced')).toBeInTheDocument();
    });

    it('shows loading skeleton when isLoading is true', () => {
      vi.mocked(sharedLogic.useHealth).mockReturnValue({
        data: undefined,
        isLoading: true,
        error: null,
      } as any);

      const { container } = render(<SettingsView />);
      const skeletons = container.querySelectorAll('.animate-pulse');
      expect(skeletons.length).toBeGreaterThan(0);
    });

    it('shows error message when fetch fails', () => {
      vi.mocked(sharedLogic.useHealth).mockReturnValue({
        data: undefined,
        isLoading: false,
        error: new Error('Connection refused'),
      } as any);

      render(<SettingsView />);
      expect(screen.getByText(/Connection refused/)).toBeInTheDocument();
    });
  });

  // ------------------------------------------------------------------
  // Notifications section
  // ------------------------------------------------------------------

  describe('Notifications section', () => {
    it('renders the Notifications heading', () => {
      render(<SettingsView />);
      expect(screen.getByText('Notifications')).toBeInTheDocument();
    });

    it('shows "Not requested" when permission is default', () => {
      render(<SettingsView />);
      expect(screen.getByText('Not requested')).toBeInTheDocument();
    });

    it('shows "Enabled" when permission is granted', () => {
      vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
        permission: 'granted',
        isIdle: false,
        requestPermission: vi.fn(),
      });

      render(<SettingsView />);
      expect(screen.getByText('Enabled')).toBeInTheDocument();
    });

    it('shows "Blocked by browser" when permission is denied', () => {
      vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
        permission: 'denied',
        isIdle: false,
        requestPermission: vi.fn(),
      });

      render(<SettingsView />);
      expect(screen.getByText('Blocked by browser')).toBeInTheDocument();
    });

    it('renders enable button when permission is default', () => {
      render(<SettingsView />);
      expect(screen.getByRole('button', { name: 'Enable browser notifications' })).toBeInTheDocument();
    });

    it('calls requestPermission when enable button is clicked', () => {
      const requestPermission = vi.fn().mockResolvedValue(true);
      vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
        permission: 'default',
        isIdle: false,
        requestPermission,
      });

      render(<SettingsView />);
      fireEvent.click(screen.getByRole('button', { name: 'Enable browser notifications' }));
      expect(requestPermission).toHaveBeenCalledOnce();
    });

    it('does not render enable button when permission is granted', () => {
      vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
        permission: 'granted',
        isIdle: false,
        requestPermission: vi.fn(),
      });

      render(<SettingsView />);
      expect(screen.queryByRole('button', { name: 'Enable browser notifications' })).not.toBeInTheDocument();
    });

    it('shows a hint message when permission is denied', () => {
      vi.mocked(sharedLogic.useNotificationState).mockReturnValue({
        permission: 'denied',
        isIdle: false,
        requestPermission: vi.fn(),
      });

      render(<SettingsView />);
      expect(screen.getByText(/Allow them in your browser site settings/)).toBeInTheDocument();
    });
  });

  // ------------------------------------------------------------------
  // Theme section
  // ------------------------------------------------------------------

  describe('Theme section', () => {
    it('renders the Theme heading', () => {
      render(<SettingsView />);
      expect(screen.getByText('Theme')).toBeInTheDocument();
    });

    it('renders system, light, and dark buttons', () => {
      render(<SettingsView />);
      expect(screen.getByRole('button', { name: /system/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /light/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /dark/i })).toBeInTheDocument();
    });

    it('marks the active theme button with aria-pressed=true', () => {
      vi.mocked(sharedLogic.useTheme).mockReturnValue({
        theme: 'dark',
        setTheme: vi.fn(),
      });

      render(<SettingsView />);
      expect(screen.getByRole('button', { name: /dark/i })).toHaveAttribute('aria-pressed', 'true');
      expect(screen.getByRole('button', { name: /light/i })).toHaveAttribute('aria-pressed', 'false');
    });

    it('calls setTheme with the correct value when a button is clicked', () => {
      const setTheme = vi.fn();
      vi.mocked(sharedLogic.useTheme).mockReturnValue({
        theme: 'system',
        setTheme,
      });

      render(<SettingsView />);
      fireEvent.click(screen.getByRole('button', { name: /light/i }));
      expect(setTheme).toHaveBeenCalledWith('light');
    });

    it('has an accessible group label for the theme buttons', () => {
      render(<SettingsView />);
      expect(screen.getByRole('group', { name: 'Theme selection' })).toBeInTheDocument();
    });
  });

  // ------------------------------------------------------------------
  // Keyboard Shortcuts section
  // ------------------------------------------------------------------

  describe('Keyboard Shortcuts section', () => {
    it('renders the Keyboard Shortcuts heading', () => {
      render(<SettingsView />);
      expect(screen.getByText('Keyboard Shortcuts')).toBeInTheDocument();
    });

    it('renders a table with an accessible label', () => {
      render(<SettingsView />);
      expect(screen.getByRole('table', { name: 'Keyboard shortcuts reference' })).toBeInTheDocument();
    });

    it('shows all number navigation shortcuts', () => {
      render(<SettingsView />);
      expect(screen.getByText('Live Feed')).toBeInTheDocument();
      expect(screen.getByText('My Inbox')).toBeInTheDocument();
      expect(screen.getByText('First Group')).toBeInTheDocument();
      expect(screen.getByText('First Agent')).toBeInTheDocument();
      // "Settings" appears in page heading and shortcut table — assert at least two occurrences
      expect(screen.getAllByText('Settings').length).toBeGreaterThanOrEqual(2);
    });

    it('shows modifier shortcut descriptions', () => {
      render(<SettingsView />);
      expect(screen.getByText('Focus search / main content')).toBeInTheDocument();
      expect(screen.getByText('Dismiss / focus main content')).toBeInTheDocument();
    });

    it('renders kbd elements for each key', () => {
      const { container } = render(<SettingsView />);
      const kbdElements = container.querySelectorAll('kbd');
      expect(kbdElements.length).toBeGreaterThan(0);
    });

    it('shows Cmd+K shortcut keys', () => {
      render(<SettingsView />);
      expect(screen.getByText('Cmd')).toBeInTheDocument();
      expect(screen.getByText('K')).toBeInTheDocument();
    });

    it('shows the Esc key', () => {
      render(<SettingsView />);
      expect(screen.getByText('Esc')).toBeInTheDocument();
    });
  });

  // ------------------------------------------------------------------
  // Overall structure
  // ------------------------------------------------------------------

  describe('overall layout', () => {
    it('renders the Settings page heading', () => {
      render(<SettingsView />);
      expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Settings');
    });

    it('renders all four sections', () => {
      render(<SettingsView />);
      expect(screen.getByText('Daemon Status')).toBeInTheDocument();
      expect(screen.getByText('Notifications')).toBeInTheDocument();
      expect(screen.getByText('Theme')).toBeInTheDocument();
      expect(screen.getByText('Keyboard Shortcuts')).toBeInTheDocument();
    });
  });
});
