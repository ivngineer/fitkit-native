import { ArrowLeft, KeyRound, Pencil, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { toast } from 'sonner';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { EmployeeForm, MIN_PASSWORD } from '@/components/EmployeeForm';
import { Avatar, Empty, ErrorNote, RoleBadges, Stat, useLoad } from '@/components/shared';
import { api } from '@/api/client';
import { STATUS_LABEL, type EmployeeDetail } from '@/api/types';
import { fmtDate } from '@/lib/format';
import { cn } from '@/lib/utils';

export function EmployeePage() {
  const { id = '' } = useParams();
  const { data, error, reload } = useLoad(() => api.employee(id), [id]);

  return (
    <>
      <Link to="/employees" className="mb-5 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Співробітники
      </Link>
      {error && <ErrorNote error={error} onRetry={reload} />}
      {data && <Detail e={data} reload={reload} />}
    </>
  );
}

function Detail({ e, reload }: { e: EmployeeDetail; reload: () => void }) {
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [pwOpen, setPwOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [leadBusy, setLeadBusy] = useState(false);
  const active = e.stats.inProgress;

  const toggleLead = async (on: boolean) => {
    setLeadBusy(true);
    try {
      await api.updateEmployee(e.id, { name: e.name, email: e.email, role: e.role, isTeamLead: on });
      toast.success(on ? `${e.name} тепер тімлід` : `${e.name} більше не тімлід`);
      reload();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Не вдалося зберегти');
    } finally {
      setLeadBusy(false);
    }
  };

  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-center gap-4">
        <Avatar name={e.name} className="size-14 text-base" />
        <div className="min-w-0 flex-1">
          <h1 className="text-2xl font-semibold tracking-tight">{e.name}</h1>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
            <span>{e.email}</span>
            <RoleBadges e={e} />
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => setEditing(true)}>
            <Pencil /> Змінити дані
          </Button>
          <Button variant="secondary" onClick={() => setPwOpen(true)}>
            <KeyRound /> Змінити пароль
          </Button>
          <Button variant="destructive" onClick={() => setDeleting(true)}>
            <Trash2 /> Видалити
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <Stat label="Виконано" value={e.stats.completed} hint="Доставлено отримувачу" tone="success" />
        <Stat label="В роботі" value={e.stats.inProgress} hint="Разом із чекають клієнта" />
        <Stat label="В дорозі" value={e.stats.onTheWay} hint="Викуплено або відправлено" />
        <Stat label="Скасовано" value={e.stats.cancelled} />
        <Stat label="Скарги" value={e.stats.complaints} tone="destructive" />
      </div>

      <label className="flex cursor-pointer items-center justify-between gap-4 rounded-3xl border bg-card px-6 py-4">
        <span>
          <span className="block font-medium">Тімлід</span>
          <span className="block text-sm text-muted-foreground">Може передавати замовлення іншим співробітникам</span>
        </span>
        <Switch checked={e.isTeamLead} disabled={leadBusy} onCheckedChange={(c) => void toggleLead(c)} aria-label="Тімлід" />
      </label>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card className="gap-0 pb-2">
          <CardHeader className="pb-4">
            <CardTitle>Останні замовлення</CardTitle>
            <CardDescription>Нові зверху</CardDescription>
          </CardHeader>
          <CardContent className="px-2">
            {e.orders.length === 0 ? (
              <Empty>Замовлень немає</Empty>
            ) : (
              <ul className="grid">
                {e.orders.map((o) => (
                  <li key={o.id} className="flex items-center gap-3 rounded-2xl px-4 py-2.5">
                    <span className="tabular w-16 text-sm font-medium">{o.number}</span>
                    <span className="min-w-0 flex-1 truncate text-sm">{o.clientName}</span>
                    <Badge
                      variant="secondary"
                      className={cn(o.status === 'delivered' && 'bg-success/15 text-success', o.status === 'cancelled' && 'text-muted-foreground')}
                    >
                      {STATUS_LABEL[o.status] ?? o.status}
                    </Badge>
                    <span className="hidden w-28 text-right text-xs text-muted-foreground sm:block">{fmtDate(o.createdAt)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card className="gap-0 pb-2">
          <CardHeader className="pb-4">
            <CardTitle>Скарги</CardTitle>
            <CardDescription>Клієнти повідомили про проблему із замовленням</CardDescription>
          </CardHeader>
          <CardContent className="px-2">
            {e.complaints.length === 0 ? (
              <Empty>Скарг немає</Empty>
            ) : (
              <ul className="grid gap-1">
                {e.complaints.map((c) => (
                  <li key={c.id} className="rounded-2xl px-4 py-2.5">
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span className="font-medium text-foreground">{c.orderNumber}</span>
                      <span>{c.clientName}</span>
                      <span className="ml-auto">{fmtDate(c.at)}</span>
                    </div>
                    <p className="mt-1 text-sm break-words whitespace-pre-wrap">{c.text}</p>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Дані співробітника</DialogTitle>
            <DialogDescription>Пошта — це логін для входу в робочий застосунок.</DialogDescription>
          </DialogHeader>
          <EmployeeForm
            initial={{ name: e.name, email: e.email, role: e.role, isTeamLead: e.isTeamLead }}
            submitLabel="Зберегти"
            onCancel={() => setEditing(false)}
            onSubmit={async (v) => {
              await api.updateEmployee(e.id, { name: v.name, email: v.email, role: v.role, isTeamLead: v.isTeamLead });
              setEditing(false);
              toast.success('Збережено');
              reload();
            }}
          />
        </DialogContent>
      </Dialog>

      <PasswordDialog open={pwOpen} onOpenChange={setPwOpen} id={e.id} name={e.name} />

      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Видалити {e.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Доступ до робочого застосунку закриється одразу.
              {active > 0 && ` Активні замовлення (${active}) повернуться в чергу.`} Історія й статистика збережуться.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Скасувати</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/85"
              onClick={async () => {
                try {
                  await api.deleteEmployee(e.id);
                  toast.success(`${e.name} видалено`);
                  navigate('/employees');
                } catch (err) {
                  toast.error(err instanceof Error ? err.message : 'Не вдалося видалити');
                }
              }}
            >
              Видалити
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function PasswordDialog({ open, onOpenChange, id, name }: { open: boolean; onOpenChange: (o: boolean) => void; id: string; name: string }) {
  const [pw, setPw] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const close = (o: boolean) => {
    if (!o) {
      setPw('');
      setError(null);
    }
    onOpenChange(o);
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Новий пароль</DialogTitle>
          <DialogDescription>{name} вийде з усіх пристроїв і увійде з новим паролем.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={async (ev) => {
            ev.preventDefault();
            if (pw.length < MIN_PASSWORD) {
              setError(`Пароль — щонайменше ${MIN_PASSWORD} символів`);
              return;
            }
            setBusy(true);
            try {
              await api.setPassword(id, pw);
              toast.success('Пароль змінено');
              close(false);
            } catch (err) {
              setError(err instanceof Error ? err.message : 'Не вдалося змінити пароль');
            } finally {
              setBusy(false);
            }
          }}
        >
          <div className="grid gap-2">
            <Label htmlFor="new-password">Пароль</Label>
            <Input
              id="new-password"
              type="text"
              autoComplete="new-password"
              autoFocus
              placeholder={`Щонайменше ${MIN_PASSWORD} символів`}
              value={pw}
              onChange={(ev) => setPw(ev.target.value)}
            />
          </div>
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>
              Скасувати
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? 'Зберігаємо…' : 'Змінити пароль'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
