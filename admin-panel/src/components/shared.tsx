import { Crown } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { initials } from '@/lib/format';
import { ROLE_LABEL, type Employee } from '@/api/types';

/** Loads data once and on reload(); keeps the last good value while reloading. */
export function useLoad<T>(load: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const run = useCallback(load, deps);
  const reload = useCallback(() => {
    run()
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Помилка'));
  }, [run]);
  useEffect(reload, [reload]);
  return { data, error, reload };
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  return (
    <span className={cn('grid size-9 shrink-0 place-items-center rounded-full bg-secondary text-xs font-semibold text-secondary-foreground', className)}>
      {initials(name)}
    </span>
  );
}

export function RoleBadges({ e }: { e: Pick<Employee, 'role' | 'isTeamLead'> }) {
  return (
    <span className="inline-flex flex-wrap gap-1.5">
      <Badge variant="secondary">{ROLE_LABEL[e.role] ?? e.role}</Badge>
      {e.isTeamLead && (
        <Badge variant="outline" className="border-warning/50 text-warning">
          <Crown /> Тімлід
        </Badge>
      )}
    </span>
  );
}

export function Stat({ label, value, hint, tone }: { label: string; value: number | string; hint?: string; tone?: 'success' | 'destructive' }) {
  return (
    <Card className="gap-0 py-5">
      <CardContent className="px-5">
        <div className="text-sm text-muted-foreground">{label}</div>
        <div
          className={cn(
            'tabular mt-1.5 text-3xl font-semibold tracking-tight',
            tone === 'success' && 'text-success',
            tone === 'destructive' && value !== 0 && 'text-destructive',
          )}
        >
          {value}
        </div>
        {hint && <div className="mt-1 text-xs text-muted-foreground">{hint}</div>}
      </CardContent>
    </Card>
  );
}

export function ErrorNote({ error, onRetry }: { error: string; onRetry: () => void }) {
  return (
    <div className="rounded-2xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
      {error}{' '}
      <button className="underline underline-offset-4" onClick={onRetry}>
        Спробувати ще раз
      </button>
    </div>
  );
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <div className="px-5 py-10 text-center text-sm text-muted-foreground">{children}</div>;
}
