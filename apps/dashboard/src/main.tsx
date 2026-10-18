import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@cofy-x/axv-nextstep/styles.css';
import './app/styles.css';
import { App } from './app/App';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
