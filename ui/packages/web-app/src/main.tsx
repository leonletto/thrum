import { StrictMode, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { wsClient } from '@thrum/shared-logic';
import './index.css';
import App from './App.tsx';
import { ErrorBoundary } from './components/ErrorBoundary.tsx';
import { AuthProvider } from './components/AuthProvider.tsx';

// Create a query client instance
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 2,
      retryDelay: 1000,
    },
  },
});

// WebSocket connection component
function WebSocketManager({ children }: { children: React.ReactNode }) {
  useEffect(() => {
    // Connect to WebSocket on mount
    wsClient.connect().catch((error) => {
      console.error('Failed to connect to WebSocket:', error);
    });

    // Cleanup on unmount
    return () => {
      wsClient.disconnect();
    };
  }, []);

  return <>{children}</>;
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <WebSocketManager>
          <AuthProvider>
            <App />
          </AuthProvider>
        </WebSocketManager>
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>
);
