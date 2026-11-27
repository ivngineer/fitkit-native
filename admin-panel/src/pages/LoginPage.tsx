import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useAuth } from '@/auth';

export function LoginPage() {
  const { signIn } = useAuth();
  const [login, setLogin] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <div className="grid min-h-svh place-items-center px-4">
      <div className="flex w-full max-w-sm flex-col items-center gap-6">
        <div className="flex items-center gap-2">
          <img src="/admin/assets/logo-mark.png" alt="" className="size-9 dark:invert" />
          <span className="font-serif text-4xl leading-none font-medium tracking-tight">Fitkit</span>
        </div>
        <Card className="w-full">
          <CardHeader>
            <CardTitle>Адмінка</CardTitle>
            <CardDescription>Логін і пароль адміністратора</CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="grid gap-4"
              onSubmit={async (e) => {
                e.preventDefault();
                setBusy(true);
                setError(null);
                try {
                  await signIn(login.trim(), password);
                } catch (err) {
                  setError(err instanceof Error ? err.message : 'Не вдалося увійти');
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-2">
                <Label htmlFor="login">Логін</Label>
                <Input id="login" autoComplete="username" autoFocus required value={login} onChange={(e) => setLogin(e.target.value)} />
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
