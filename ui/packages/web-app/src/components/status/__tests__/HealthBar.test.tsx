import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@/test/test-utils';
import { HealthBar } from '../HealthBar';
import * as sharedLogic from '@thrum/shared-logic';

// Mock the useHealth hook
vi.mock('@thrum/shared-logic', () => ({
  useHealth: vi.fn(),
}));

describe('HealthBar', () => {
  it('renders connected status when health data is available', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.2.3',
        uptime_ms: 9252000,
        repo_id: 'abc123def456ghi789',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    render(<HealthBar />);

    expect(screen.getByText('CONNECTED')).toBeInTheDocument();
    expect(screen.getByText(/1\.2\.3/)).toBeInTheDocument();
    expect(screen.getByText(/2h 34m/)).toBeInTheDocument();
    expect(screen.getByText(/abc123de/)).toBeInTheDocument();
  });

  it('renders disconnected status when error occurs', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Connection failed'),
    } as any);

    render(<HealthBar />);

    expect(screen.getByText('DISCONNECTED')).toBeInTheDocument();
    expect(screen.queryByText(/v/)).not.toBeInTheDocument();
  });

  it('renders disconnected status when health status is not ok', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'degraded',
        version: '1.2.3',
        uptime_ms: 9252000,
        repo_id: 'abc123def456',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    render(<HealthBar />);

    expect(screen.getByText('DISCONNECTED')).toBeInTheDocument();
    expect(screen.queryByText(/1\.2\.3/)).not.toBeInTheDocument();
  });

  it('shows green indicator when connected', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    const { container } = render(<HealthBar />);

    const indicator = container.querySelector('.bg-green-500');
    expect(indicator).toBeInTheDocument();
  });

  it('shows red indicator when disconnected', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Failed'),
    } as any);

    const { container } = render(<HealthBar />);

    const indicator = container.querySelector('.bg-red-500');
    expect(indicator).toBeInTheDocument();
  });

  it('formats uptime from milliseconds', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 12342000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    render(<HealthBar />);

    expect(screen.getByText(/3h 25m/)).toBeInTheDocument();
  });

  it('truncates repo ID to 8 characters', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'abcdefghijklmnop',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    render(<HealthBar />);

    expect(screen.getByText(/abcdefgh/)).toBeInTheDocument();
    expect(screen.queryByText(/ijklmnop/)).not.toBeInTheDocument();
  });

  it('has correct accessibility attributes', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    render(<HealthBar />);

    const footer = screen.getByRole('contentinfo');
    expect(footer).toHaveAttribute('aria-label', 'System health status');
  });

  it('applies cyberpunk styling', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    const { container } = render(<HealthBar />);

    const footer = container.querySelector('footer');
    expect(footer).toHaveClass('bg-[#0a0e1a]');
    expect(footer).toHaveClass('border-t');
    expect(footer).toHaveClass('border-cyan-500/10');
    expect(footer).toHaveClass('font-mono');
    expect(footer).toHaveClass('text-[11px]');
  });

  it('positions footer at bottom with fixed positioning', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    const { container } = render(<HealthBar />);

    const footer = container.querySelector('footer');
    expect(footer).toHaveClass('fixed');
    expect(footer).toHaveClass('bottom-0');
    expect(footer).toHaveClass('left-0');
    expect(footer).toHaveClass('right-0');
  });

  it('has correct height', () => {
    vi.mocked(sharedLogic.useHealth).mockReturnValue({
      data: {
        status: 'ok',
        version: '1.0.0',
        uptime_ms: 3600000,
        repo_id: 'test123',
        sync_state: 'synced',
      },
      isLoading: false,
      error: null,
    } as any);

    const { container } = render(<HealthBar />);

    const footer = container.querySelector('footer');
    expect(footer).toHaveClass('h-8');
  });
});
