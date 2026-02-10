import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ThrumWebSocket } from './websocket';
import { ConnectionState, WebSocketConnectionError } from '../types/websocket';

// Mock WebSocket
class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  url: string;
  onopen: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;

  private listeners = new Map<string, Set<EventListener>>();

  constructor(url: string) {
    this.url = url;
  }

  addEventListener(type: string, listener: EventListener): void {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, new Set());
    }
    this.listeners.get(type)!.add(listener);
  }

  removeEventListener(type: string, listener: EventListener): void {
    this.listeners.get(type)?.delete(listener);
  }

  dispatchEvent(event: Event): boolean {
    const listeners = this.listeners.get(event.type);
    if (listeners) {
      listeners.forEach((listener) => listener(event));
    }
    return true;
  }

  send(data: string): void {
    if (this.readyState !== MockWebSocket.OPEN) {
      throw new Error('WebSocket is not open');
    }
    (this as any).lastSent = data;
  }

  close(code?: number, reason?: string): void {
    if (this.readyState !== MockWebSocket.CLOSED) {
      this.readyState = MockWebSocket.CLOSED;
      const event = new CloseEvent('close', { code, reason });
      this.dispatchEvent(event);
    }
  }

  simulateMessage(data: string): void {
    const event = new MessageEvent('message', { data });
    this.dispatchEvent(event);
  }

  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN;
    this.dispatchEvent(new Event('open'));
  }

  simulateError(): void {
    this.dispatchEvent(new Event('error'));
  }
}

// Setup global WebSocket mock
let currentMockWs: MockWebSocket | null = null;
(global as any).WebSocket = class extends MockWebSocket {
  constructor(url: string) {
    super(url);
    currentMockWs = this;
  }
};

describe('ThrumWebSocket', () => {
  let client: ThrumWebSocket;

  beforeEach(() => {
    currentMockWs = null;
    client = new ThrumWebSocket({
      url: 'ws://localhost:9842',
      reconnectDelayMs: 10,
      maxReconnectDelayMs: 100,
      requestTimeoutMs: 1000,
    });
  });

  afterEach(() => {
    if (client) {
      client.disconnect();
    }
  });

  describe('connect', () => {
    it('should connect successfully', async () => {
      const connectPromise = client.connect();

      expect(client.state).toBe(ConnectionState.CONNECTING);
      expect(currentMockWs).toBeTruthy();

      // Simulate successful connection
      currentMockWs!.simulateOpen();

      await connectPromise;

      expect(client.isConnected).toBe(true);
      expect(client.state).toBe(ConnectionState.CONNECTED);
    });

    it('should handle connection errors', async () => {
      const connectPromise = client.connect();

      // Simulate error before connection opens
      currentMockWs!.simulateError();

      await expect(connectPromise).rejects.toThrow(WebSocketConnectionError);

      // Note: State will be RECONNECTING because scheduleReconnect is called
      // This is expected behavior - the client attempts to reconnect
    });

    it('should not create new connection if already open', async () => {
      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;

      const firstWs = currentMockWs;
      await client.connect();

      expect(currentMockWs).toBe(firstWs);
    });
  });

  describe('disconnect', () => {
    it('should disconnect and clean up', async () => {
      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;

      client.disconnect();

      expect(client.isConnected).toBe(false);
      expect(client.state).toBe(ConnectionState.DISCONNECTED);
    });

    it('should reject pending requests on disconnect', async () => {
      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;

      const callPromise = client.call('test.method');
      client.disconnect();

      await expect(callPromise).rejects.toThrow('Connection closed');
    });
  });

  describe('call', () => {
    beforeEach(async () => {
      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;
    });

    it('should make a successful JSON-RPC call', async () => {
      const callPromise = client.call('test.method', { param: 'value' });

      // Verify request was sent
      const sent = JSON.parse((currentMockWs as any).lastSent);
      expect(sent.jsonrpc).toBe('2.0');
      expect(sent.method).toBe('test.method');
      expect(sent.params).toEqual({ param: 'value' });
      expect(sent.id).toBeDefined();

      // Simulate response
      currentMockWs!.simulateMessage(
        JSON.stringify({
          jsonrpc: '2.0',
          result: { success: true },
          id: sent.id,
        })
      );

      const result = await callPromise;
      expect(result).toEqual({ success: true });
    });

    it('should handle JSON-RPC error response', async () => {
      const callPromise = client.call('test.method');

      const sent = JSON.parse((currentMockWs as any).lastSent);
      currentMockWs!.simulateMessage(
        JSON.stringify({
          jsonrpc: '2.0',
          error: {
            code: -32600,
            message: 'Invalid request',
          },
          id: sent.id,
        })
      );

      await expect(callPromise).rejects.toThrow('Invalid request');
    });

    it('should timeout if no response received', async () => {
      const callPromise = client.call('test.method');

      // Wait for timeout
      await expect(callPromise).rejects.toThrow('Request timeout');
    }, 2000);

    it('should throw error if not connected', async () => {
      client.disconnect();

      await expect(client.call('test.method')).rejects.toThrow(
        'Not connected'
      );
    });
  });

  describe('event subscriptions', () => {
    beforeEach(async () => {
      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;
    });

    it('should handle event notifications', () => {
      const handler = vi.fn();
      client.on('message.received', handler);

      currentMockWs!.simulateMessage(
        JSON.stringify({
          jsonrpc: '2.0',
          method: 'message.received',
          params: { message: 'Hello' },
        })
      );

      expect(handler).toHaveBeenCalledWith({ message: 'Hello' });
    });

    it('should support multiple handlers for same event', () => {
      const handler1 = vi.fn();
      const handler2 = vi.fn();

      client.on('test.event', handler1);
      client.on('test.event', handler2);

      currentMockWs!.simulateMessage(
        JSON.stringify({
          jsonrpc: '2.0',
          method: 'test.event',
          params: { data: 'test' },
        })
      );

      expect(handler1).toHaveBeenCalledWith({ data: 'test' });
      expect(handler2).toHaveBeenCalledWith({ data: 'test' });
    });

    it('should unsubscribe handler', () => {
      const handler = vi.fn();
      const unsubscribe = client.on('test.event', handler);

      unsubscribe();

      currentMockWs!.simulateMessage(
        JSON.stringify({
          jsonrpc: '2.0',
          method: 'test.event',
          params: {},
        })
      );

      expect(handler).not.toHaveBeenCalled();
    });
  });

  describe('connection state', () => {
    it('should notify state change handlers', async () => {
      const stateHandler = vi.fn();
      client.onStateChange(stateHandler);

      // Should be called immediately with current state
      expect(stateHandler).toHaveBeenCalledWith(ConnectionState.DISCONNECTED);

      const connectPromise = client.connect();
      expect(stateHandler).toHaveBeenCalledWith(ConnectionState.CONNECTING);

      currentMockWs!.simulateOpen();
      await connectPromise;

      expect(stateHandler).toHaveBeenCalledWith(ConnectionState.CONNECTED);
    });

    it('should unsubscribe from state changes', async () => {
      const stateHandler = vi.fn();
      const unsubscribe = client.onStateChange(stateHandler);

      stateHandler.mockClear();
      unsubscribe();

      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;

      // Should not be called after unsubscribe
      expect(stateHandler).not.toHaveBeenCalled();
    });
  });

  describe('reconnection', () => {
    it('should transition to reconnecting state on connection loss', async () => {
      const stateChanges: ConnectionState[] = [];
      client.onStateChange((state) => stateChanges.push(state));

      const connectPromise = client.connect();
      currentMockWs!.simulateOpen();
      await connectPromise;

      expect(client.state).toBe(ConnectionState.CONNECTED);

      // Clear previous state changes
      stateChanges.length = 0;

      // Simulate connection close
      currentMockWs!.close();

      // Wait for reconnect state to be set
      await new Promise((resolve) => setTimeout(resolve, 10));

      // Should transition to DISCONNECTED then RECONNECTING
      expect(stateChanges).toContain(ConnectionState.DISCONNECTED);
      expect(client.state).toBe(ConnectionState.RECONNECTING);
    }, 500);

    it('should track connection state changes', async () => {
      const states: ConnectionState[] = [];
      client.onStateChange((state) => states.push(state));

      const connectPromise = client.connect();
      expect(states).toContain(ConnectionState.CONNECTING);

      currentMockWs!.simulateOpen();
      await connectPromise;

      expect(states).toContain(ConnectionState.CONNECTED);
      expect(states.length).toBeGreaterThanOrEqual(2); // DISCONNECTED -> CONNECTING -> CONNECTED
    }, 500);
  });
});
