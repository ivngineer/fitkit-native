import { Navigate, Route, Routes } from 'react-router-dom';
import { Layout } from '@/components/Layout';
import { BoardPage } from '@/pages/BoardPage';
import { ClientsPage } from '@/pages/ClientsPage';
import { OrderPage } from '@/pages/OrderPage';
import { RequestsPage } from '@/pages/RequestsPage';
import { TrackingPage } from '@/pages/TrackingPage';
import { LoginPage, SplashScreen } from '@/pages/LoginPage';
import { useAuth } from '@/store/AppStore';
import { useSettings } from '@/store/Settings';

export function App() {
  const { settings } = useSettings();
  const { phase, retry } = useAuth();
  if (phase === 'loading') return <SplashScreen />;
  if (phase === 'offline') return <SplashScreen offline onRetry={retry} />;
  if (phase === 'signedOut') return <LoginPage />;
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Navigate to="/orders" replace />} />
        <Route path="requests" element={settings.showRequests ? <RequestsPage /> : <Navigate to="/orders" replace />} />
        <Route path="orders" element={<BoardPage />} />
        <Route path="orders/:id" element={<OrderPage />} />
        <Route path="clients" element={<ClientsPage />} />
        <Route path="tracking" element={<TrackingPage />} />
        <Route path="*" element={<Navigate to="/orders" replace />} />
      </Route>
    </Routes>
  );
}
