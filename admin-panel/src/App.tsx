import { Navigate, Route, Routes } from 'react-router-dom';
import { Shell } from '@/components/Shell';
import { AnalyticsPage } from '@/pages/AnalyticsPage';
import { EmployeePage } from '@/pages/EmployeePage';
import { EmployeesPage } from '@/pages/EmployeesPage';
import { LoginPage } from '@/pages/LoginPage';
import { useAuth } from './auth';

export function App() {
  const { state } = useAuth();
  if (state.status === 'loading') return <div className="min-h-svh" />;
  if (state.status === 'out') return <LoginPage />;
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<AnalyticsPage />} />
        <Route path="employees" element={<EmployeesPage />} />
        <Route path="employees/:id" element={<EmployeePage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
