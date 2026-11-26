import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Theme = 'dark' | 'light';
export interface Settings {
  theme: Theme;
  /** The “Заявки” (incoming requests) tab is hidden unless turned on. */
  showRequests: boolean;
}

const KEY = 'fitkit.settings';
const DEFAULTS: Settings = { theme: 'dark', showRequests: false };

function load(): Settings {
  try {
    const raw = localStorage.getItem(KEY);
    return raw ? { ...DEFAULTS, ...JSON.parse(raw) } : DEFAULTS;
  } catch {
    return DEFAULTS;
  }
}

const Ctx = createContext<{ settings: Settings; update: (patch: Partial<Settings>) => void } | null>(null);

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(load);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', settings.theme === 'dark');
    try {
      localStorage.setItem(KEY, JSON.stringify(settings));
    } catch {
      /* private mode — settings live for this session only */
    }
  }, [settings]);

  const value = useMemo(() => ({ settings, update: (patch: Partial<Settings>) => setSettings((s) => ({ ...s, ...patch })) }), [settings]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSettings() {
  const c = useContext(Ctx);
  if (!c) throw new Error('useSettings outside SettingsProvider');
  return c;
}
