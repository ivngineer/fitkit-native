import { AlertTriangle, Trophy } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { PageTitle } from '@/components/Shell';
import { Avatar, Empty, ErrorNote, RoleBadges, Stat, useLoad } from '@/components/shared';
import { api } from '@/api/client';
import type { LeaderboardRow } from '@/api/types';
import { plural } from '@/lib/format';
import { cn } from '@/lib/utils';

export function AnalyticsPage() {
  const { data, error, reload } = useLoad(() => api.analytics(), []);
  const top = data?.leaderboard.find((r) => r.count > 0);

  return (
    <>
      <PageTitle title="Аналітика" subtitle="Виконане замовлення — те, що вже доставлене отримувачу." />
      {error && <ErrorNote error={error} onRetry={reload} />}
      {data && (
        <div className="grid gap-6">
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
            <Stat label="Співробітників" value={data.totals.employees} />
            <Stat label="Замовлень усього" value={data.totals.orders} />
            <Stat label="Виконано" value={data.totals.delivered} tone="success" />
            <Stat label="В роботі" value={data.totals.inProgress} />
            <Stat label="Скарг" value={data.totals.complaints} tone="destructive" />
          </div>

          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex flex-wrap items-center gap-4 bg-secondary/50 px-6 py-5">
              <span className="grid size-11 place-items-center rounded-full bg-warning/15 text-warning">
                <Trophy className="size-5" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-sm text-muted-foreground">Найбільше виконаних замовлень</div>
                {top ? (
                  <Link to={`/employees/${top.employee.id}`} className="text-xl font-semibold tracking-tight hover:underline">
                    {top.employee.name}
                  </Link>
                ) : (
                  <div className="text-xl font-semibold text-muted-foreground">Поки нікого</div>
                )}
              </div>
              {top && (
                <div className="text-right">
                  <div className="tabular text-3xl font-semibold text-success">{top.count}</div>
                  <div className="text-xs text-muted-foreground">{plural(top.count, 'замовлення', 'замовлення', 'замовлень')}</div>
                </div>
              )}
            </div>
          </Card>

          <div className="grid gap-6 lg:grid-cols-2">
            <Card className="gap-0 pb-2">
              <CardHeader className="pb-4">
                <CardTitle>Лідерборд</CardTitle>
                <CardDescription>За кількістю виконаних замовлень</CardDescription>
              </CardHeader>
              <CardContent className="px-2">
                {data.leaderboard.length === 0 ? <Empty>Співробітників ще немає</Empty> : <Board rows={data.leaderboard} />}
              </CardContent>
            </Card>

            <Card className="gap-0 pb-2">
              <CardHeader className="pb-4">
                <CardTitle className="flex items-center gap-2">
                  <AlertTriangle className="size-4 text-destructive" /> Найбільше скарг
                </CardTitle>
                <CardDescription>Скарги клієнтів із мобільного застосунку на замовлення</CardDescription>
              </CardHeader>
              <CardContent className="px-2">
                {data.mostComplaints.length === 0 ? <Empty>Скарг немає</Empty> : <Board rows={data.mostComplaints} danger />}
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </>
  );
}

function Board({ rows, danger }: { rows: LeaderboardRow[]; danger?: boolean }) {
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <ol className="grid">
      {rows.map((r, i) => (
        <li key={r.employee.id}>
          <Link to={`/employees/${r.employee.id}`} className="flex items-center gap-3 rounded-2xl px-4 py-2.5 transition-colors hover:bg-secondary">
            <span className="tabular w-5 text-right text-sm text-muted-foreground">{i + 1}</span>
            <Avatar name={r.employee.name} className="size-8" />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium">{r.employee.name}</span>
                <span className="hidden sm:contents">
                  <RoleBadges e={r.employee} />
                </span>
              </div>
              <div className="mt-1.5 h-1 rounded-full bg-secondary">
                <div
                  className={cn('h-1 rounded-full', danger ? 'bg-destructive' : 'bg-success')}
                  style={{ width: `${(r.count / max) * 100}%` }}
                />
              </div>
            </div>
            <span className="tabular w-8 text-right text-sm font-semibold">{r.count}</span>
          </Link>
        </li>
      ))}
    </ol>
  );
}
