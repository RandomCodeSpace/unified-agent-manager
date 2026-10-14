// Event streams, over EventSource or, on plain HTTP, over the service's event socket.
//
// Over HTTP/1.1 a browser allows six connections per host, and every tab holds an event stream or more (a Task's,
// the connected notices, each connected instance's), so a few tabs hold them all and every request stalls.
// WebSockets have a pool of their own: `/api/events/socket` carries the same stream, served by the same handler,
// and SocketEventSource reads it as an EventSource would. HTTPS (HTTP/2) shares one connection and needs none of it.
//
// Either way the stream is watched for silence: a connection the network dropped without a close (a half-open TCP
// connection after a Wi-Fi or mobile handover, a laptop back from sleep, a proxy that stopped forwarding) stays
// "open" for minutes while nothing arrives. The service writes a heartbeat every KEEP_ALIVE_MS; a stream that has
// carried nothing for STREAM_SILENCE_MS is dropped, reported as an error while connecting, and reopened.

let sockets = false;
/** Set when sockets fail before any opened on this page (a proxy that does not pass them): EventSource from then on. */
let socketsFailed = false;
let socketOpened = false;

/** The service writes a heartbeat on every stream this often (internal/web's heartbeatInterval). */
export const KEEP_ALIVE_MS = 15_000;
/** A stream that has carried nothing for this long, two and a half heartbeats, is dead whatever the connection says. */
export const STREAM_SILENCE_MS = 2.5 * KEEP_ALIVE_MS;

/** Event streams go over the event socket from now on. */
export function enableSocketStreams(): void {
  sockets = true;
}

/** An event stream of this service at `url` (a path, as the API client builds it). */
export function openEventStream(url: string): EventSource {
  return new SocketEventSource(url) as unknown as EventSource;
}

/** The event socket's address for the stream at `url`, on the page's host. */
export function eventSocketUrl(location: { protocol: string; host: string }, url: string): string {
  const target = new URL(url, `${location.protocol}//${location.host}`);
  return `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/events/socket?path=${encodeURIComponent(target.pathname + target.search)}`;
}

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/** The events an EventSource reports through `onopen` and `onerror`, read from there and never as listeners too. */
const handled = (type: string) => type === 'open' || type === 'error';

/**
 * The stream at `url` read from the event socket the way EventSource reads it: `open` once the stream's status is
 * 200; an `error` and CLOSED, without retrying, on any other status; an `error` and CONNECTING, then a reconnect after
 * the stream's `retry` time, when the connection drops or falls silent. Named events and `message` carry their data;
 * comments and ids are not used by the service's streams. Lines end in LF (with an optional CR before it), as the
 * service writes. While sockets are off or have failed, the same stream over EventSource, watched for silence too.
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
  /** The silence watch: reset by every byte over the socket, by every heartbeat event over EventSource. */
  private quiet: ReturnType<typeof setTimeout> | undefined;
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
    if (this.native && !this.types.has(type) && !handled(type)) this.native.addEventListener(type, this.forward(type));
    this.types.add(type);
  }

  close(): void {
    this.readyState = CLOSED;
    clearTimeout(this.timer);
    clearTimeout(this.quiet);
    this.socket?.close();
    this.native?.close();
  }

  private connect(): void {
    if (this.readyState === CLOSED) return;
    if (socketsFailed || !sockets) return this.fallBack();
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
        this.heard();
        this.fire(new Event('open'));
        return;
      }
      this.heard();
      this.feed(typeof event.data === 'string' ? event.data : this.decoder.decode(event.data as ArrayBuffer, { stream: true }));
    };
    socket.onclose = () => {
      if (this.socket !== socket || this.readyState === CLOSED) return;
      // No socket has worked on this page: something on the way refuses them. EventSource takes over, here and for later streams.
      if (status === undefined && !socketOpened) socketsFailed = true;
      this.dropped(socketsFailed ? 0 : this.retry);
    };
  }

  /**
   * The same stream over EventSource, its events passed on as this source's. `open` and `error` come through the
   * handler properties: a browser fires each once either way, and a stand-in may only call the property.
   */
  private fallBack(): void {
    const native = new EventSource(this.url);
    this.native = native;
    native.onopen = this.forward('open');
    native.onerror = this.forward('error');
    for (const type of new Set([...this.types, 'message', 'keep-alive'])) if (!handled(type)) native.addEventListener(type, this.forward(type));
  }

  /** The native's events of `type` passed on as this source's (by the name they were listened for, which is the event's). */
  private forward(type: string): (event: Event) => void {
    return (event) => {
      if (this.native === null || this.readyState === CLOSED) return;
      // EventSource never shows a comment, so the service sends its heartbeats as events; a stream whose heartbeats
      // are comments (an older service's, another instance's notices) is never taken for silent.
      if (type === 'keep-alive') return this.heard();
      if (handled(type)) {
        this.readyState = this.native.readyState;
        this.fire(new Event(type));
      } else this.fire(new MessageEvent(type, { data: (event as MessageEvent).data }));
    };
  }

  /** The connection is gone: listeners hear an error, and the stream reopens after `delay` unless one closed it. */
  private dropped(delay: number): void {
    clearTimeout(this.quiet);
    this.socket = null;
    this.native = null;
    this.readyState = CONNECTING;
    this.fire(new Event('error'));
    if (this.readyState === CONNECTING) this.timer = setTimeout(() => this.connect(), delay);
  }

  private heard(): void {
    clearTimeout(this.quiet);
    this.quiet = setTimeout(() => this.stalled(), STREAM_SILENCE_MS);
  }

  /** Nothing for STREAM_SILENCE_MS: the connection is dead, however it looks. */
  private stalled(): void {
    const { socket, native } = this;
    if (socket) {
      // Its close event, if it ever comes, is not this stream's any more.
      this.socket = null;
      socket.close();
    } else if (native) {
      this.native = null;
      native.close();
    } else return;
    this.dropped(this.retry);
  }

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
