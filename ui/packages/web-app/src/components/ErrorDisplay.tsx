import { AlertCircle } from 'lucide-react';
import { Button } from './ui/button';

interface ErrorDisplayProps {
  error?: Error;
  onRetry?: () => void;
  title?: string;
  message?: string;
}

/**
 * Error display component
 *
 * Shows a user-friendly error message with optional retry button.
 *
 * Example:
 * ```tsx
 * <ErrorDisplay
 *   error={new Error('Failed to load data')}
 *   onRetry={() => refetch()}
 * />
 * ```
 *
 * Custom message:
 * ```tsx
 * <ErrorDisplay
 *   title="Connection Error"
 *   message="Unable to connect to server"
 *   onRetry={handleRetry}
 * />
 * ```
 */
export function ErrorDisplay({
  error,
  onRetry,
  title = 'Something went wrong',
  message,
}: ErrorDisplayProps) {
  const displayMessage = message || error?.message || 'An unexpected error occurred';

  return (
    <div className="flex flex-col items-center justify-center p-8 text-center">
      <AlertCircle className="h-12 w-12 text-destructive mb-4" />
      <h2 className="text-lg font-semibold">{title}</h2>
      <p className="text-muted-foreground mt-2">{displayMessage}</p>
      {onRetry && (
        <Button onClick={onRetry} className="mt-4">
          Try again
        </Button>
      )}
    </div>
  );
}
