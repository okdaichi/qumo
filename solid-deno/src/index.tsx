/* @refresh reload */
import { render } from 'solid-js/web'
import { ConfigProvider } from './context/index.ts'
import './index.css'
import App from './App.tsx'

const root = document.getElementById('root')

render(
  () => (
    <ConfigProvider>
      <App />
    </ConfigProvider>
  ),
  root!
)
