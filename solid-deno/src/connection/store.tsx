import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { MoQConnection } from "./client.ts";
import type { ConnectionStatus } from "../types.ts";

interface ConnectionState {
  status: ConnectionStatus;
  error?: string;
  url?: string;
}

// Global connection store
const [state, setState] = createStore<ConnectionState>({
  status: "disconnected",
});

let connection: MoQConnection | null = null;

// Initialize connection instance
function getConnection(): MoQConnection {
  if (!connection) {
    connection = new MoQConnection((status, error) => {
      setState({ status, error: error || undefined });
    });
  }
  return connection;
}

export function useConnection() {
  const connect = async (url: string): Promise<void> => {
    const conn = getConnection();
    setState({ url });
    await conn.connect({ url });
  };

  const disconnect = async (): Promise<void> => {
    const conn = getConnection();
    await conn.disconnect();
    setState({ url: undefined });
  };

  const getSession = () => {
    return connection?.getSession() || null;
  };

  const getMux = () => {
    return connection?.getMux() || null;
  };

  return {
    state,
    connect,
    disconnect,
    getSession,
    getMux,
  };
}
