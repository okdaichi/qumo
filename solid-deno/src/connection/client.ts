import { Client, Session, TrackMux } from "@okdaichi/moq";
import type { ConnectionStatus } from "../types.ts";

export interface MoQConnectionOptions {
  url: string;
  versions?: Set<number>;
  reconnect?: boolean;
}

export class MoQConnection {
  private client: Client | null = null;
  private session: Session | null = null;
  private mux: TrackMux | null = null;
  private url: string = "";
  private statusCallback?: (status: ConnectionStatus, error?: string) => void;

  constructor(onStatusChange?: (status: ConnectionStatus, error?: string) => void) {
    this.statusCallback = onStatusChange;
  }

  async connect(options: MoQConnectionOptions): Promise<void> {
    try {
      this.statusCallback?.("connecting");
      this.url = options.url;

      // Create client and mux
      this.client = new Client({
        versions: options.versions || new Set([1]),
        reconnect: options.reconnect ?? false,
      });
      this.mux = new TrackMux();

      // Dial to create session
      this.session = await this.client.dial(options.url, this.mux);
      await this.session.ready;

      this.statusCallback?.("connected");
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.statusCallback?.("error", message);
      throw error;
    }
  }

  async disconnect(): Promise<void> {
    try {
      if (this.session) {
        await this.session.close();
        this.session = null;
      }
      if (this.client) {
        await this.client.close();
        this.client = null;
      }
      this.mux = null;
      this.statusCallback?.("disconnected");
    } catch (error) {
      console.error("Disconnect error:", error);
      this.statusCallback?.("disconnected");
    }
  }

  getSession(): Session | null {
    return this.session;
  }

  getMux(): TrackMux | null {
    return this.mux;
  }

  isConnected(): boolean {
    return this.session !== null;
  }
}
