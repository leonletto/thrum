/**
 * Skip to main content link for accessibility
 *
 * Allows keyboard users to skip navigation and jump directly to main content.
 * Hidden by default, visible when focused with Tab key.
 */
export function SkipLink() {
  return (
    <a
      href="#main-content"
      className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-50 focus:px-4 focus:py-2 focus:bg-background focus:border focus:rounded-md"
    >
      Skip to main content
    </a>
  );
}
