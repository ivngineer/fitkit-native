import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { App } from './App';
import { AuthProvider } from './auth';
import '@fontsource-variable/newsreader';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename="/admin">
      <TooltipProvider delayDuration={300}>
        <AuthProvider>
          <App />
          <Toaster />
        </AuthProvider>
      </TooltipProvider>
    </BrowserRouter>
  </StrictMode>,
);
