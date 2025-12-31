import { createContext, type ParentComponent, useContext } from "solid-js";
import { createStore, type SetStoreFunction } from "solid-js/store";

export interface AppConfig {
  relayUrl: string;
  apiUrl: string;
  appName: string;
  isDev: boolean;
}

interface ConfigContextValue {
  config: AppConfig;
  setConfig: SetStoreFunction<AppConfig>;
}

const ConfigContext = createContext<ConfigContextValue>();

export const ConfigProvider: ParentComponent = (props) => {
  const [config, setConfig] = createStore<AppConfig>({
    relayUrl: import.meta.env.VITE_RELAY_URL || "https://localhost:6670",
    apiUrl: import.meta.env.VITE_API_URL || "http://localhost:8080",
    appName: import.meta.env.VITE_APP_NAME || "qumo",
    isDev: import.meta.env.DEV,
  });

  return (
    <ConfigContext.Provider value={{ config, setConfig }}>
      {props.children}
    </ConfigContext.Provider>
  );
};

export function useConfig(): ConfigContextValue {
  const context = useContext(ConfigContext);
  if (!context) {
    throw new Error("useConfig must be used within a ConfigProvider");
  }
  return context;
}
