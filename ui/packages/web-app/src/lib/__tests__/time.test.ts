import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { formatRelativeTime } from '../time';

describe('formatRelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2024-01-01T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  test('returns "just now" for times less than 60 seconds ago', () => {
    const timestamp = new Date('2024-01-01T11:59:30Z').toISOString();
    expect(formatRelativeTime(timestamp)).toBe('just now');
  });

  test('returns minutes for times less than 1 hour ago', () => {
    const timestamp = new Date('2024-01-01T11:55:00Z').toISOString();
    expect(formatRelativeTime(timestamp)).toBe('5m ago');
  });

  test('returns hours for times less than 24 hours ago', () => {
    const timestamp = new Date('2024-01-01T09:00:00Z').toISOString();
    expect(formatRelativeTime(timestamp)).toBe('3h ago');
  });

  test('returns days for times 24 hours or more ago', () => {
    const timestamp = new Date('2023-12-30T12:00:00Z').toISOString();
    expect(formatRelativeTime(timestamp)).toBe('2d ago');
  });
});
