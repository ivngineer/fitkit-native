import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Count, FilterTabs, Thumbs, type Tone } from '@/components/shared';
import { itemsCount, timeLeft, uah } from '@/domain/format';
import type { Order, OrderStatus } from '@/domain/types';
import { cn } from '@/lib/utils';
import { useLookups, useStore } from '@/store/AppStore';

const COLS: { status: OrderStatus; label: string; tone: Tone }[] = [
  { status: 'in_progress', label: 'В роботі', tone: 'neutral' },
  { status: 'awaiting', label: 'Чекає підтвердження', tone: 'warn' },
  { status: 'purchased', label: 'Викуплено', tone: 'info' },
  { status: 'shipped', label: 'Відправлено', tone: 'ok' },
];
const DOT: Record<Tone, string> = { neutral: 'bg-muted-foreground', info: 'bg-foreground', warn: 'bg-warning', bad: 'bg-destructive', ok: 'bg-success' };

function note(o: Order): { tone: 'bad' | 'warn'; text: string } | null {
  if (o.problem) return { tone: 'bad', text: o.problem };
  const a = o.items.find((i) => i.status === 'awaiting');
  if (a && a.price && a.storePrice) return { tone: 'warn', text: `Ціна +${Math.round(((a.storePrice - a.price) / a.price) * 100)}% — запит надіслано` };
  return null;
}

export function BoardPage() {
  const { state, now, moveOrder } = useStore();
  const { client } = useLookups();
  const navigate = useNavigate();
  const [scope, setScope] = useState<'mine' | 'all'>('mine');
  const [drag, setDrag] = useState<string | null>(null);
  const [over, setOver] = useState<OrderStatus | null>(null);

  const list = state.orders.filter((o) => COLS.some((c) => c.status === o.status) && (scope === 'all' || o.ownerId === state.me));

  return (
    <>
      <div className="flex items-center gap-4">
        <h1 className="flex-1 text-2xl font-semibold tracking-tight">
          Поточні замовлення <Count>{list.length}</Count>
        </h1>
        <FilterTabs value={scope} onChange={setScope} tabs={[{ key: 'mine', label: 'Мої' }, { key: 'all', label: 'Усі' }]} />
      </div>
      <p className="-mt-3 text-sm text-muted-foreground">Перетягніть картку в іншу колонку, щоб змінити статус.</p>
      <div className="grid grid-cols-4 items-start gap-4">
        {COLS.map((col) => {
          const cards = list.filter((o) => o.status === col.status).sort((a, b) => Number(!!b.problem) - Number(!!a.problem) || a.dueAt.localeCompare(b.dueAt));
          return (
            <div
              key={col.status}
              className={cn('flex min-h-48 flex-col gap-2.5 rounded-3xl border bg-card p-2.5 transition-colors', over === col.status && 'border-input bg-accent/40')}
              onDragOver={(e) => {
                e.preventDefault();
                setOver(col.status);
              }}
              onDragLeave={() => setOver(null)}
              onDrop={(e) => {
                e.preventDefault();
                setOver(null);
                if (drag) moveOrder(drag, col.status);
                setDrag(null);
              }}
            >
              <div className="flex items-center gap-2 px-2.5 pt-1.5 pb-1 text-sm font-medium">
                <span className={cn('size-2 rounded-full', DOT[col.tone])} />
                {col.label}
                <Count>{cards.length}</Count>
              </div>
              {cards.map((o) => {
                const n = note(o);
                const t = timeLeft(o.dueAt, now);
                const info = o.status === 'purchased' || o.status === 'shipped' ? o.location ?? '' : `${itemsCount(o.items.length)} · ${o.paid != null ? uah(o.paid) : 'ціна не вказана'}`;
                return (
                  <button
                    key={o.id}
                    draggable
                    onDragStart={() => setDrag(o.id)}
                    onDragEnd={() => setDrag(null)}
                    onClick={() => navigate(`/orders/${o.id}`)}
                    className={cn(
                      'flex cursor-grab flex-col gap-3 rounded-2xl bg-secondary p-3.5 text-left transition-colors hover:bg-accent active:cursor-grabbing',
                      drag === o.id && 'opacity-40',
                    )}
                  >
                    <div className="flex w-full items-center gap-3">
                      <Thumbs items={o.items} />
                      <span className="tabular font-semibold">{o.number}</span>
                      <span className={cn('tabular ml-auto text-xs font-medium whitespace-nowrap text-muted-foreground', t.tone === 'red' && 'text-destructive')}>{t.short}</span>
                    </div>
                    <div className="text-sm">
                      <div>{client(o.clientId).name}</div>
                      <div className="text-muted-foreground">{info}</div>
                    </div>
                    {n && (
                      <div className="flex items-start gap-2 text-[13px] text-muted-foreground">
                        <span className={cn('mt-1.5 size-1.5 shrink-0 rounded-full', n.tone === 'bad' ? 'bg-destructive' : 'bg-warning')} />
                        {n.text}
                      </div>
                    )}
                  </button>
                );
              })}
            </div>
          );
        })}
      </div>
    </>
  );
}
