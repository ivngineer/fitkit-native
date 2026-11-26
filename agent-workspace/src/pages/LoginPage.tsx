import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useAuth } from '@/store/AppStore';

export function LoginPage() {
  const { login } = useAuth();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <div className="grid min-h-screen place-items-center px-4">
      <div className="flex w-full max-w-sm flex-col items-center gap-6">
        <div className="flex items-center gap-2">
          <img src="/assets/logo-mark.png" alt="" className="size-9 dark:invert" />
          <span className="font-serif text-4xl leading-none font-medium tracking-tight">Fitkit</span>
        </div>
        <Card className="w-full">
          <CardHeader>
            <CardTitle>Вхід для співробітників</CardTitle>
            <CardDescription>Робоча пошта й пароль від Fitkit</CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="grid gap-4"
              onSubmit={async (e) => {
                e.preventDefault();
                setBusy(true);
                setError(await login(email.trim(), password));
                setBusy(false);
              }}
            >
              <div className="grid gap-2">
                <Label htmlFor="email">Пошта</Label>
                <Input id="email" type="email" autoComplete="username" autoFocus required value={email} onChange={(e) => setEmail(e.target.value)} />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="password">Пароль</Label>
                <Input id="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              </div>
              {error && (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              )}
              <Button type="submit" size="lg" disabled={busy}>
                {busy ? 'Входимо…' : 'Увійти'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export function SplashScreen({ offline, onRetry }: { offline?: boolean; onRetry?: () => void }) {
  return (
    <div className="grid min-h-screen place-items-center px-4 text-center">
      <div className="flex flex-col items-center gap-4">
        <img src="/assets/logo-mark.png" alt="" className={`size-10 dark:invert ${offline ? '' : 'animate-pulse'}`} />
        {offline ? (
          <>
            <div>
              <div className="font-semibold">Сервер недоступний</div>
              <div className="text-sm text-muted-foreground">Перевірте, що API запущено, і спробуйте ще раз.</div>
            </div>
            <Button variant="secondary" onClick={onRetry}>
              Спробувати ще раз
            </Button>
          </>
        ) : (
          <div className="text-sm text-muted-foreground">Завантаження…</div>
        )}
      </div>
    </div>
  );
}
