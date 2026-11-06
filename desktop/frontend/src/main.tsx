import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/outfit'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'

import App from './App'
import './styles.css'

const root = document.getElementById('root')
if (!root) {
  throw new Error('the window has no root element')
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
