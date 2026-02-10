import { useEffect, useState } from 'react';
import { wsClient } from '../api/client';
import { ConnectionState } from '../types/websocket';

/**
 * Hook to access WebSocket connection state
 *
 * Returns the current connection state and methods to connect/disconnect.
 *
 * Example:
 * ```tsx
 * function ConnectionStatus() {
 *   const { state, isConnected, connect, disconnect } = useWebSocket();
 *
 *   return (
 *     <div>
 *       Status: {state}
 *       {!isConnected && <button onClick={connect}>Connect</button>}
 *     </div>
 *   );
 * }
 * ```
 */
export function useWebSocket() {
  const [state, setState] = useState<ConnectionState>(wsClient.state);
  const [isConnected, setIsConnected] = useState(wsClient.isConnected);

  useEffect(() => {
    const unsubscribe = wsClient.onStateChange((newState) => {
      setState(newState);
      setIsConnected(wsClient.isConnected);
    });

    return unsubscribe;
  }, []);

  return {
    state,
    isConnected,
    connect: () => wsClient.connect(),
    disconnect: () => wsClient.disconnect(),
    client: wsClient,
  };
}

/**
 * Hook to subscribe to WebSocket events
 *
 * Automatically subscribes to the specified event and calls the handler
 * when events are received. Cleans up on unmount.
 *
 * Example:
 * ```tsx
 * function MessageListener() {
 *   useWebSocketEvent('message.created', (data) => {
 *     console.log('New message:', data);
 *   });
 *
 *   return <div>Listening for messages...</div>;
 * }
 * ```
 */
export function useWebSocketEvent<T = unknown>(
  event: string,
  handler: (data: T) => void
) {
  useEffect(() => {
    const unsubscribe = wsClient.on<T>(event, handler);
    return unsubscribe;
  }, [event, handler]);
}
