import { BarChart3, LogOut, Users } from 'lucide-react';
import { NavLink, Outlet } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { useAuth } from '@/auth';

const NAV = [
  { to: '/', label: 'Аналітика', icon: BarChart3, end: true },
  { to: '/employees', label: 'Співробітники', icon: Users, end: false },
];

export function Shell() {
  const { state, signOut } = useAuth();
  const login = state.status === 'in' ? state.login : '';
  return (
    <div className="min-h-svh">
      <header className="sticky top-0 z-20 border-b bg-background/85 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4 sm:px-6">
          <div className="flex items-center gap-2.5">
            <img src="/admin/assets/logo-mark.png" alt="" className="size-6 dark:invert" />
            <span className="font-serif text-2xl leading-none font-medium tracking-tight">Fitkit</span>
            <span className="rounded-full bg-secondary px-2 py-0.5 text-xs text-muted-foreground">адмінка</span>
          </div>
          <nav className="flex items-center gap-1">
            {NAV.map(({ to, label, icon: Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    'flex h-9 items-center gap-2 rounded-full px-3.5 text-sm transition-colors',
                    isActive ? 'bg-secondary text-foreground' : 'text-muted-foreground hover:text-foreground',
                  )
                }
              >
                <Icon className="size-4" />
                <span className="hidden sm:inline">{label}</span>
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-2">
            <span className="hidden text-sm text-muted-foreground sm:inline">{login}</span>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Вийти" onClick={() => void signOut()}>
                  <LogOut />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Вийти</TooltipContent>
            </Tooltip>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
        <Outlet />
      </main>
    </div>
  );
}

export function PageTitle({ title, subtitle, children }: { title: string; subtitle?: string; children?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>}
      </div>
      {children}
    </div>
  );
}
