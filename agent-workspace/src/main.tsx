import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { App } from './App';
import { AppStoreProvider } from './store/AppStore';
import { SettingsProvider } from './store/Settings';
import '@fontsource-variable/newsreader';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <SettingsProvider>
      <BrowserRouter>
        <TooltipProvider delayDuration={300}>
          <AppStoreProvider>
            <App />
            <Toaster />
          </AppStoreProvider>
        </TooltipProvider>
      </BrowserRouter>
    </SettingsProvider>
  </StrictMode>,
);
