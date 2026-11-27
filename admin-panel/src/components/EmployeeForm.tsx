import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { ROLE_LABEL, type EmployeeInput, type Role } from '@/api/types';

export const MIN_PASSWORD = 10;

/** Name, email (= login), role and the team lead switch; password only when creating. */
export function EmployeeForm({
  initial,
  withPassword,
  submitLabel,
  onSubmit,
  onCancel,
}: {
  initial: EmployeeInput;
  withPassword?: boolean;
  submitLabel: string;
  onSubmit: (v: EmployeeInput) => Promise<void>;
  onCancel?: () => void;
}) {
  const [v, setV] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof EmployeeInput>(k: K, val: EmployeeInput[K]) => setV((p) => ({ ...p, [k]: val }));

  return (
    <form
      className="grid gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        if (withPassword && (v.password ?? '').length < MIN_PASSWORD) {
          setError(`Пароль — щонайменше ${MIN_PASSWORD} символів`);
          return;
        }
        setBusy(true);
        setError(null);
        try {
          await onSubmit({ ...v, name: v.name.trim(), email: v.email.trim() });
        } catch (err) {
          setError(err instanceof Error ? err.message : 'Не вдалося зберегти');
        } finally {
          setBusy(false);
        }
      }}
    >
      <div className="grid gap-2">
        <Label htmlFor="ef-name">Ім’я</Label>
        <Input id="ef-name" required value={v.name} onChange={(e) => set('name', e.target.value)} />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="ef-email">Логін (робоча пошта)</Label>
        <Input id="ef-email" type="email" required autoComplete="off" value={v.email} onChange={(e) => set('email', e.target.value)} />
      </div>
      {withPassword && (
        <div className="grid gap-2">
          <Label htmlFor="ef-password">Пароль</Label>
          <Input
            id="ef-password"
            type="text"
            required
            autoComplete="new-password"
            placeholder={`Щонайменше ${MIN_PASSWORD} символів`}
            value={v.password ?? ''}
            onChange={(e) => set('password', e.target.value)}
          />
          <p className="text-xs text-muted-foreground">Передайте пароль співробітнику — пізніше його можна змінити.</p>
        </div>
      )}
      <div className="grid gap-2">
        <Label>Роль</Label>
        <div role="radiogroup" className="grid grid-cols-3 gap-1 rounded-full bg-secondary p-1">
          {(Object.keys(ROLE_LABEL) as Role[]).map((r) => (
            <button
              key={r}
              type="button"
              role="radio"
              aria-checked={v.role === r}
              onClick={() => set('role', r)}
              className={cn(
                'h-8 rounded-full text-sm transition-colors',
                v.role === r ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {ROLE_LABEL[r]}
            </button>
          ))}
        </div>
      </div>
      <label className="flex cursor-pointer items-start justify-between gap-4 rounded-2xl bg-secondary/60 px-4 py-3">
        <span>
          <span className="block text-sm font-medium">Тімлід</span>
          <span className="block text-xs text-muted-foreground">Може передавати замовлення іншим співробітникам</span>
        </span>
        <Switch checked={v.isTeamLead} onCheckedChange={(c) => set('isTeamLead', c)} aria-label="Тімлід" />
      </label>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-2">
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel}>
            Скасувати
          </Button>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? 'Зберігаємо…' : submitLabel}
        </Button>
      </div>
    </form>
  );
}
