import { Plus, Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { EmployeeForm } from '@/components/EmployeeForm';
import { PageTitle } from '@/components/Shell';
import { Avatar, Empty, ErrorNote, RoleBadges, useLoad } from '@/components/shared';
import { api } from '@/api/client';
import { cn } from '@/lib/utils';

export function EmployeesPage() {
  const { data, error, reload } = useLoad(() => api.employees(), []);
  const [adding, setAdding] = useState(false);
  const [q, setQ] = useState('');
  const navigate = useNavigate();

  const list = useMemo(() => {
    const s = q.trim().toLowerCase();
    return (data ?? []).filter((e) => !s || e.name.toLowerCase().includes(s) || e.email.toLowerCase().includes(s));
  }, [data, q]);

  return (
    <>
      <PageTitle title="Співробітники" subtitle="Доступ до робочого застосунку. Реєстрації немає — акаунти створюються тут.">
        <Button onClick={() => setAdding(true)}>
          <Plus /> Додати
        </Button>
      </PageTitle>

      {error && <ErrorNote error={error} onRetry={reload} />}
      {data && (
        <Card className="gap-0 py-0">
          <div className="border-b px-4 py-3">
            <div className="relative max-w-xs">
              <Search className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input placeholder="Пошук за ім’ям або поштою" className="pl-10" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
          </div>
          {list.length === 0 ? (
            <Empty>{data.length === 0 ? 'Співробітників ще немає' : 'Нікого не знайдено'}</Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Співробітник</TableHead>
                  <TableHead>Роль</TableHead>
                  <TableHead className="text-right">Виконано</TableHead>
                  <TableHead className="text-right">В роботі</TableHead>
                  <TableHead className="text-right">В дорозі</TableHead>
                  <TableHead className="text-right">Скарги</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((e) => (
                  <TableRow key={e.id} className="cursor-pointer hover:bg-secondary/60" onClick={() => navigate(`/employees/${e.id}`)}>
                    <TableCell>
                      <div className="flex items-center gap-3">
                        <Avatar name={e.name} />
                        <div>
                          <div className="font-medium">{e.name}</div>
                          <div className="text-xs text-muted-foreground">{e.email}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <RoleBadges e={e} />
                    </TableCell>
                    <TableCell className="tabular text-right font-semibold text-success">{e.stats.completed}</TableCell>
                    <TableCell className="tabular text-right">{e.stats.inProgress}</TableCell>
                    <TableCell className="tabular text-right">{e.stats.onTheWay}</TableCell>
                    <TableCell className={cn('tabular text-right', e.stats.complaints > 0 && 'font-semibold text-destructive')}>
                      {e.stats.complaints}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Card>
      )}

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Новий співробітник</DialogTitle>
            <DialogDescription>Співробітник увійде в робочий застосунок із цією поштою й паролем.</DialogDescription>
          </DialogHeader>
          <EmployeeForm
            initial={{ name: '', email: '', role: 'buyer', isTeamLead: false, password: '' }}
            withPassword
            submitLabel="Додати"
            onCancel={() => setAdding(false)}
            onSubmit={async (v) => {
              const e = await api.createEmployee(v);
              setAdding(false);
              toast.success(`${e.name} додано`);
              reload();
            }}
          />
        </DialogContent>
      </Dialog>
    </>
  );
}
