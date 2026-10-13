// Event streams, over EventSource or, on plain HTTP, over the service's event socket.
//
// Over HTTP/1.1 a browser allows six connections per host, and every tab holds an event stream or more (a Task's,
// the connected notices, each connected instance's), so a few tabs hold them all and every request stalls.
// WebSockets have a pool of their own: `/api/events/socket` carries the same stream, served by the same handler,
// and SocketEventSource reads it as an EventSource would. HTTPS (HTTP/2) shares one connection and needs none of it.

let sockets = false;
/** Set when sockets fail before any opened on this page (a proxy that does not pass them): EventSource from then on. */
let socketsFailed = false;
let socketOpened = false;

/** Event streams go over the event socket from now on. */
export function enableSocketStreams(): void {
  sockets = true;
}

/** An event stream of this service at `url` (a path, as the API client builds it). */
export function openEventStream(url: string): EventSource {
  return sockets && !socketsFailed ? new SocketEventSource(url) as unknown as EventSource : new EventSource(url);
}

/** The event socket's address for the stream at `url`, on the page's host. */
export function eventSocketUrl(location: { protocol: string; host: string }, url: string): string {
  const target = new URL(url, `${location.protocol}//${location.host}`);
  return `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/events/socket?path=${encodeURIComponent(target.pathname + target.search)}`;
}

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/**
 * The stream at `url` read from the event socket the way EventSource reads it: `open` once the stream's status is
 * 200; an `error` and CLOSED, without retrying, on any other status; an `error` and CONNECTING, then a reconnect after
 * the stream's `retry` time, when the connection drops. Named events and `message` carry their data; comments and
 * ids are not used by the service's streams. Lines end in LF (with an optional CR before it), as the service writes.
 */
export class SocketEventSource extends EventTarget {
  static readonly CONNECTING = CONNECTING;
  static readonly OPEN = OPEN;
  static readonly CLOSED = CLOSED;
  readonly CONNECTING = CONNECTING;
  readonly OPEN = OPEN;
  readonly CLOSED = CLOSED;
  readonly url: string;
  readonly withCredentials = false;
  readyState: number = CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  private socket: WebSocket | null = null;
  private native: EventSource | null = null;
  private readonly types = new Set<string>();
  private timer: ReturnType<typeof setTimeout> | undefined;
  private retry = 3000;
  private decoder = new TextDecoder();
  private buffer = '';
  private type = '';
  private data: string[] = [];

  constructor(url: string) {
    super();
    this.url = url;
    this.connect();
  }

  override addEventListener(type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions): void {
    super.addEventListener(type, listener, options);
    if (this.native && !this.types.has(type)) this.native.addEventListener(type, this.forward);
    this.types.add(type);
  }

  close(): void {
    this.readyState = CLOSED;
    clearTimeout(this.timer);
    this.socket?.close();
    this.native?.close();
  }

  private connect(): void {
    if (this.readyState === CLOSED) return;
    if (socketsFailed) return this.fallBack();
    this.decoder = new TextDecoder();
    this.buffer = '';
    this.type = '';
    this.data = [];
    let status: number | undefined;
    const socket = new WebSocket(eventSocketUrl(window.location, this.url));
    socket.binaryType = 'arraybuffer';
    this.socket = socket;
    socket.onmessage = (event: MessageEvent) => {
      if (this.socket !== socket || this.readyState === CLOSED) return;
      if (status === undefined) {
        status = Number(event.data);
        if (status !== 200) {
          this.readyState = CLOSED;
          socket.close();
          this.fire(new Event('error'));
          return;
        }
        socketOpened = true;
        this.readyState = OPEN;
        this.fire(new Event('open'));
        return;
      }
      this.feed(typeof event.data === 'string' ? event.data : this.decoder.decode(event.data as ArrayBuffer, { stream: true }));
    };
    socket.onclose = () => {
      if (this.socket !== socket || this.readyState === CLOSED) return;
      this.socket = null;
      // No socket has worked on this page: something on the way refuses them. EventSource takes over, here and for later streams.
      if (status === undefined && !socketOpened) socketsFailed = true;
      this.readyState = CONNECTING;
      this.fire(new Event('error'));
      if (this.readyState === CONNECTING) this.timer = setTimeout(() => this.connect(), socketsFailed ? 0 : this.retry);
    };
  }

  /** The same stream over EventSource, its events passed on as this source's. */
  private fallBack(): void {
    const native = new EventSource(this.url);
    this.native = native;
    for (const type of new Set([...this.types, 'open', 'error', 'message'])) native.addEventListener(type, this.forward);
  }

  private readonly forward = (event: Event) => {
    if (this.native === null || this.readyState === CLOSED) return;
    if (event.type === 'open' || event.type === 'error') {
      this.readyState = this.native.readyState;
      this.fire(new Event(event.type));
    } else if (event instanceof MessageEvent) this.fire(new MessageEvent(event.type, { data: event.data }));
  };

  private fire(event: Event): void {
    this.dispatchEvent(event);
    if (event.type === 'open') this.onopen?.(event);
    else if (event.type === 'error') this.onerror?.(event);
    else if (event.type === 'message') this.onmessage?.(event as MessageEvent);
  }

  private feed(text: string): void {
    this.buffer += text;
    let start = 0;
    for (let end = this.buffer.indexOf('\n'); end >= 0 && this.readyState === OPEN; end = this.buffer.indexOf('\n', start)) {
      const line = this.buffer.slice(start, this.buffer[end - 1] === '\r' ? end - 1 : end);
      start = end + 1;
      this.line(line);
    }
    this.buffer = this.buffer.slice(start);
  }

  private line(line: string): void {
    if (line === '') {
      if (this.data.length) this.fire(new MessageEvent(this.type || 'message', { data: this.data.join('\n') }));
      this.type = '';
      this.data = [];
      return;
    }
    if (line.startsWith(':')) return;
    const colon = line.indexOf(':');
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') this.type = value;
    else if (field === 'data') this.data.push(value);
    else if (field === 'retry' && /^\d+$/.test(value)) this.retry = Number(value);
  }
}

/** For tests: forget what this page learnt about sockets. */
export function resetSocketStreams(): void {
  sockets = false;
  socketsFailed = false;
  socketOpened = false;
}
