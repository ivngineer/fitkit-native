import { useState, type FormEvent } from 'react';
import { Link, NavLink, Outlet, useNavigate } from 'react-router-dom';
import { BellIcon, InboxIcon, SearchIcon, SettingsIcon, ShoppingBagIcon, TruckIcon, UsersIcon, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { SettingsDialog } from '@/components/SettingsDialog';
import { Avatar } from '@/components/shared';
import { cn } from '@/lib/utils';
import { useLookups, useStore } from '@/store/AppStore';
import { useSettings } from '@/store/Settings';

const ROLE = { buyer: 'Закупник', lead: 'Старший зміни', warehouse: 'Склад', support: 'Підтримка' } as const;

export function Layout() {
  const { state, now, toast } = useStore();
  const { order, client } = useLookups();
  const navigate = useNavigate();
  const [q, setQ] = useState('');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { settings } = useSettings();
  const me = useStore().me;

  const newReq = state.orders.filter((o) => o.status === 'new');
  const overdueReq = settings.showRequests ? newReq.filter((o) => new Date(o.dueAt).getTime() < now) : [];
  const mine = state.orders.filter((o) => o.ownerId === state.me && ['in_progress', 'awaiting', 'purchased', 'shipped'].includes(o.status));
  const openTickets = state.tickets.filter((t) => t.status === 'open');
  const trackProblems = state.shipments.filter((s) => s.problem);
  const problems = state.orders.filter((o) => o.problem && o.ownerId === state.me && o.status !== 'cancelled');

  const nav: { to: string; icon: LucideIcon; label: string; n: number; end?: boolean }[] = [
    { to: '/orders', icon: ShoppingBagIcon, label: 'Поточні замовлення', n: mine.length },
    ...(settings.showRequests ? [{ to: '/requests', icon: InboxIcon, label: 'Заявки', n: newReq.length }] : []),
    { to: '/clients', icon: UsersIcon, label: 'Клієнти', n: openTickets.length },
    { to: '/tracking', icon: TruckIcon, label: 'Відстеження', n: trackProblems.length },
  ];
  const alerts = overdueReq.length + openTickets.length + trackProblems.length + problems.length;

  function search(e: FormEvent) {
    e.preventDefault();
    const s = q.trim().toLowerCase().replace(/\s+/g, ' ');
    if (!s) return;
    const digits = s.replace(/\D/g, '');
    const num = s.startsWith('#') ? s : '#' + s;
    const byNum = state.orders.find((o) => o.number.toLowerCase() === num);
    const byTrack = digits.length >= 6 ? state.shipments.find((sh) => sh.tracking?.replace(/\D/g, '').includes(digits)) : undefined;
    const byClient = state.clients.find((c) => c.name.toLowerCase().includes(s) || (digits.length >= 4 && c.phone.replace(/\D/g, '').includes(digits)));
    if (byNum) navigate(`/orders/${byNum.id}`);
    else if (byTrack) navigate(`/tracking?order=${byTrack.orderId}`);
    else if (byClient) navigate(`/clients?client=${byClient.id}`);
    else return toast(`Нічого не знайдено за запитом «${q}»`, 'bad');
    setQ('');
  }

  return (
    <div className="flex min-h-screen min-w-[1280px]">
      <aside className="sticky top-0 flex h-screen w-64 shrink-0 flex-col border-r bg-background px-3 py-5">
        <Link to="/orders" className="mb-6 flex items-center gap-2 px-3">
          <img src="/assets/logo-mark.png" alt="" className="size-7 dark:invert" />
          <span className="font-serif text-[26px] leading-none font-medium tracking-tight">Fitkit</span>
        </Link>
        <nav className="flex flex-col gap-0.5">
          {nav.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              className={({ isActive }) =>
                cn('flex items-center gap-3 rounded-full px-3 py-2 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground', isActive && 'bg-secondary text-foreground')
              }
            >
              <n.icon className="size-[18px] shrink-0" />
              {n.label}
              {n.n > 0 && <span className="tabular ml-auto grid h-5 min-w-5 place-items-center rounded-full bg-foreground px-1.5 text-[11px] font-semibold text-background">{n.n}</span>}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto border-t pt-3">
        <button
          onClick={() => setSettingsOpen(true)}
          className="flex w-full items-center gap-3 rounded-2xl px-2 py-2 text-left transition-colors hover:bg-secondary [&:hover_svg]:text-foreground"
          aria-label="Налаштування"
        >
          <Avatar name={me.name} />
          <div className="flex-1 text-sm leading-tight">
            <div className="font-medium">{me.name}</div>
            <div className="text-xs text-muted-foreground">{ROLE[me.role]}</div>
          </div>
          <SettingsIcon className="size-[18px] text-muted-foreground" />
        </button>
        </div>
        <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-10 flex h-16 shrink-0 items-center justify-center border-b bg-background/90 backdrop-blur">
          <form onSubmit={search} className="relative w-[440px]">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-4 size-4 -translate-y-1/2 text-muted-foreground" />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Пошук: № заявки, клієнт, телефон або ТТН"
              aria-label="Пошук"
              className="h-10 w-full rounded-full bg-secondary pr-4 pl-11 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-[3px] focus-visible:ring-ring/40"
            />
          </form>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" className="absolute right-6" aria-label="Сповіщення">
                <BellIcon className="size-[18px]" />
                {alerts > 0 && <span className="absolute top-2 right-2 size-2 rounded-full bg-foreground" />}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-80">
              <DropdownMenuLabel>Потребує уваги</DropdownMenuLabel>
              {alerts === 0 && <div className="px-2.5 py-3 text-sm text-muted-foreground">Усе під контролем.</div>}
              {overdueReq.map((o) => (
                <DropdownMenuItem key={o.id} onSelect={() => navigate(`/orders/${o.id}`)}>
                  <span className="size-1.5 rounded-full bg-destructive" />
                  Заявка {o.number} прострочена
                </DropdownMenuItem>
              ))}
              {problems.map((o) => (
                <DropdownMenuItem key={o.id} onSelect={() => navigate(`/orders/${o.id}`)}>
                  <span className="size-1.5 shrink-0 rounded-full bg-destructive" />
                  <span className="truncate">
                    {o.number}: {o.problem}
                  </span>
                </DropdownMenuItem>
              ))}
              {(openTickets.length > 0 || trackProblems.length > 0) && <DropdownMenuSeparator />}
              {openTickets.map((t) => (
                <DropdownMenuItem key={t.id} onSelect={() => navigate(`/clients?client=${t.clientId}`)}>
                  <span className="size-1.5 rounded-full bg-foreground" />
                  <span className="truncate">Звернення: {client(t.clientId).name}</span>
                </DropdownMenuItem>
              ))}
              {trackProblems.map((s) => (
                <DropdownMenuItem key={s.id} onSelect={() => navigate(`/tracking?order=${s.orderId}`)}>
                  <span className="size-1.5 rounded-full bg-warning" />
                  <span className="truncate">
                    {order(s.orderId)?.number}: {s.problem}
                  </span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </header>
        <main className="flex min-w-0 flex-1 flex-col gap-6 px-10 py-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
