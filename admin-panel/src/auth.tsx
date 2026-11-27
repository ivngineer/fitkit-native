import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import { api, setUnauthorizedHandler } from '@/api/client';

type State = { status: 'loading' } | { status: 'out' } | { status: 'in'; login: string };

interface Auth {
  state: State;
  signIn: (login: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
}

const Ctx = createContext<Auth | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ status: 'loading' });

  useEffect(() => {
    setUnauthorizedHandler(() => setState({ status: 'out' }));
    api
      .me()
      .then((r) => setState({ status: 'in', login: r.login }))
      .catch(() => setState({ status: 'out' }));
  }, []);

  const signIn = useCallback(async (login: string, password: string) => {
    const r = await api.login(login, password);
    setState({ status: 'in', login: r.login });
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      setState({ status: 'out' });
    }
  }, []);

  return <Ctx.Provider value={{ state, signIn, signOut }}>{children}</Ctx.Provider>;
}

export function useAuth() {
  const v = useContext(Ctx);
  if (!v) throw new Error('useAuth outside AuthProvider');
  return v;
}
