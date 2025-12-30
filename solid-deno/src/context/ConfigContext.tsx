import { createContext, useContext, type ParentComponent } from 'solid-js'

export interface AppConfig {
  relayUrl: string
  apiUrl: string
  appName: string
  isDev: boolean
}

const ConfigContext = createContext<AppConfig>()

export const ConfigProvider: ParentComponent = (props) => {
  const config: AppConfig = {
    relayUrl: import.meta.env.VITE_RELAY_URL || 'https://localhost:4433',
    apiUrl: import.meta.env.VITE_API_URL || 'http://localhost:8080',
    appName: import.meta.env.VITE_APP_NAME || 'Qumo',
    isDev: import.meta.env.DEV,
  }

  return (
    <ConfigContext.Provider value={config}>
      {props.children}
    </ConfigContext.Provider>
  )
}

export function useConfig(): AppConfig {
  const context = useContext(ConfigContext)
  if (!context) {
    throw new Error('useConfig must be used within a ConfigProvider')
  }
  return context
}
