/**
 * Thrum WebSocket Client Example (TypeScript)
 *
 * This example demonstrates:
 * - User registration
 * - Sending messages
 * - Event subscriptions
 * - Event handling
 */

import WebSocket from 'ws';

// Event types
interface MessageCreateEvent {
  type: 'message.create';
  message_id: string;
  agent_id: string;
  session_id: string;
  timestamp: string;
  body: {
    format: string;
    content: string;
  };
  scopes: Array<{ type: string; value: string }>;
  refs: Array<{ type: string; value: string }>;
}

// JSON-RPC types
interface JSONRPCRequest {
  jsonrpc: '2.0';
  method: string;
  params: any;
  id: number;
}

interface JSONRPCResponse {
  jsonrpc: '2.0';
  result?: any;
  error?: {
    code: number;
    message: string;
    data?: any;
  };
  id: number;
}

interface JSONRPCNotification {
  jsonrpc: '2.0';
  method: string;
  params: any;
}

class ThrumClient {
  private ws: WebSocket;
  private nextId = 1;
  private pending = new Map<number, {
    resolve: (result: any) => void;
    reject: (error: any) => void;
  }>();

  private userId?: string;
  private sessionId?: string;

  constructor(url: string) {
    this.ws = new WebSocket(url);
    this.ws.on('open', () => this.onOpen());
    this.ws.on('message', (data) => this.onMessage(data.toString()));
    this.ws.on('error', (err) => this.onError(err));
    this.ws.on('close', () => this.onClose());
  }

  private onOpen() {
    console.log('✓ Connected to Thrum daemon');
  }

  private onMessage(data: string) {
    const msg = JSON.parse(data);

    // Handle RPC response (has id field)
    if (msg.id !== undefined) {
      const pending = this.pending.get(msg.id);
      if (pending) {
        this.pending.delete(msg.id);
        if (msg.error) {
          pending.reject(new Error(`${msg.error.message} (code: ${msg.error.code})`));
        } else {
          pending.resolve(msg.result);
        }
      }
      return;
    }

    // Handle event (no id field)
    if (msg.method) {
      this.handleEvent(msg.method, msg.params);
    }
  }

  private onError(err: Error) {
    console.error('WebSocket error:', err);
  }

  private onClose() {
    console.log('✗ Disconnected from Thrum daemon');
  }

  private handleEvent(method: string, params: any) {
    switch (method) {
      case 'message.created':
        this.onMessageCreated(params as MessageCreateEvent);
        break;
      case 'message.edited':
        console.log(`Message edited: ${params.message_id}`);
        break;
      case 'message.deleted':
        console.log(`Message deleted: ${params.message_id}`);
        break;
      default:
        console.log(`Unknown event: ${method}`, params);
    }
  }

  private onMessageCreated(event: MessageCreateEvent) {
    console.log(`\n📨 New message from ${event.agent_id}:`);
    console.log(`   ${event.body.content}`);
    console.log(`   ID: ${event.message_id}`);
  }

  async call(method: string, params: any = {}): Promise<any> {
    return new Promise((resolve, reject) => {
      const id = this.nextId++;
      this.pending.set(id, { resolve, reject });

      const request: JSONRPCRequest = {
        jsonrpc: '2.0',
        method,
        params,
        id
      };

      this.ws.send(JSON.stringify(request));

      // Timeout after 30 seconds
      setTimeout(() => {
        if (this.pending.has(id)) {
          this.pending.delete(id);
          reject(new Error('Request timeout'));
        }
      }, 30000);
    });
  }

  async registerUser(username: string, displayName?: string) {
    const result = await this.call('user.register', {
      username,
      display_name: displayName
    });

    this.userId = result.user_id;
    this.sessionId = result.session_id;

    console.log(`✓ Registered as: ${result.user_id}`);
    console.log(`  Session: ${result.session_id}`);

    return result;
  }

  async sendMessage(content: string, options: {
    threadId?: string;
    scopes?: Array<{ type: string; value: string }>;
    refs?: Array<{ type: string; value: string }>;
    actingAs?: string;
  } = {}) {
    const result = await this.call('message.send', {
      content,
      thread_id: options.threadId,
      scopes: options.scopes || [],
      refs: options.refs || [],
      acting_as: options.actingAs
    });

    console.log(`✓ Message sent: ${result.message_id}`);
    return result;
  }

  async subscribe(filterType: 'scope' | 'mention' | 'all', options: {
    scope?: { type: string; value: string };
    mention?: string;
  } = {}) {
    const params: any = { filter_type: filterType };

    if (filterType === 'scope' && options.scope) {
      params.scope = options.scope;
    } else if (filterType === 'mention' && options.mention) {
      params.mention = options.mention;
    }

    const result = await this.call('subscribe.create', params);
    console.log(`✓ Subscribed: ${result.subscription_id}`);
    return result;
  }

  async listMessages(options: {
    threadId?: string;
    scope?: { type: string; value: string };
    pageSize?: number;
    page?: number;
  } = {}) {
    return await this.call('message.list', {
      thread_id: options.threadId,
      scope: options.scope,
      page_size: options.pageSize || 10,
      page: options.page || 1
    });
  }

  close() {
    this.ws.close();
  }
}

// Example usage
async function main() {
  const client = new ThrumClient('ws://localhost:9999');

  // Wait for connection
  await new Promise(resolve => setTimeout(resolve, 100));

  try {
    // 1. Register as user
    await client.registerUser('alice', 'Alice Smith');

    // 2. Subscribe to all events (for demo purposes)
    await client.subscribe('all');

    // 3. Send a message
    await client.sendMessage('Hello from the WebSocket client!', {
      scopes: [{ type: 'task', value: 'demo' }]
    });

    // 4. List recent messages
    const messages = await client.listMessages({ pageSize: 5 });
    console.log(`\n📋 Recent messages (${messages.total_count} total):`);
    for (const msg of messages.messages) {
      console.log(`   ${msg.message_id}: ${msg.body.content.substring(0, 50)}...`);
    }

    // Keep connection alive to receive events
    console.log('\n👂 Listening for events... (press Ctrl+C to exit)');

  } catch (error) {
    console.error('Error:', error);
    client.close();
  }
}

// Run if executed directly
if (require.main === module) {
  main().catch(console.error);
}

export { ThrumClient };
