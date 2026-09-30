import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './i18n'
import '@xterm/xterm/css/xterm.css'
import './index.css'
// 视觉主题层：重映射 index.css 的语义 token，对齐 Rycar1/vshell-verge 配色。
// 必须排在 index.css 之后，否则 :root 会被覆盖回原紫色方案。
import './theme-vshell.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
