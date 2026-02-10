import { expect, afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import * as matchers from '@testing-library/jest-dom/matchers';

expect.extend(matchers);

afterEach(() => {
  cleanup();
});

// Mock ResizeObserver for component tests (required by some UI libraries)
global.ResizeObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock IntersectionObserver if needed
global.IntersectionObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));

// Mock getBoundingClientRect for floating-ui components (dropdowns, tooltips)
const mockBoundingClientRect = () => ({
  width: 100,
  height: 50,
  top: 0,
  left: 0,
  bottom: 50,
  right: 100,
  x: 0,
  y: 0,
  toJSON: () => {},
});

Element.prototype.getBoundingClientRect = vi.fn(mockBoundingClientRect);
HTMLElement.prototype.getBoundingClientRect = vi.fn(mockBoundingClientRect);

// Mock Range.getBoundingClientRect for floating-ui
Range.prototype.getBoundingClientRect = vi.fn(mockBoundingClientRect);
Range.prototype.getClientRects = vi.fn(() => [mockBoundingClientRect()] as unknown as DOMRectList);

// Mock document.documentElement.getBoundingClientRect for floating-ui
if (document.documentElement) {
  document.documentElement.getBoundingClientRect = vi.fn(mockBoundingClientRect);
}

// Mock scrollIntoView for scroll components
Element.prototype.scrollIntoView = vi.fn();

// Mock window.matchMedia for theme tests
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});
