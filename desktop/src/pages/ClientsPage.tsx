import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { SendIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Avatar, CopyButton, Empty, FilterTabs, OrderStatus, StatusDot, Thumbs } from '@/components/shared';
import { QUICK_REPLIES } from '@/data/quickReplies';
import { clock, dayLabel } from '@/domain/format';
import { clientStatus } from '@/domain/rules';
import { cn } from '@/lib/utils';
import { useLookups, useStore } from '@/store/AppStore';

export function ClientsPage() {
  const { state, dispatch, now } = useStore();
  const { client, order } = useLookups();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [tab, setTab] = useState<'all' | 'open'>('open');
  const [q, setQ] = useState('');
  const [text, setText] = useState('');
  const msgsRef = useRef<HTMLDivElement>(null);

  const openCount = state.tickets.filter((t) => t.status === 'open').length;
  const rows = useMemo(
    () =>
      state.clients
        .map((c) => {
          const ts = state.tickets.filter((t) => t.clientId === c.id).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
          return { c, ticket: ts.find((t) => t.status === 'open') ?? ts[0] };
        })
        .filter((r) => (tab === 'all' ? true : r.ticket?.status === 'open'))
        .filter((r) => r.c.name.toLowerCase().includes(q.toLowerCase()))
        .sort((a, b) => (b.ticket?.updatedAt ?? '').localeCompare(a.ticket?.updatedAt ?? '')),
    [state.clients, state.tickets, tab, q],
  );

  const selectedId = params.get('client') ?? rows[0]?.c.id;
  const sel = selectedId ? client(selectedId) : undefined;
  const selTickets = state.tickets.filter((t) => t.clientId === selectedId).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  const ticket = selTickets.find((t) => t.status === 'open') ?? selTickets[0];
  const tOrder = ticket?.orderId ? order(ticket.orderId) : undefined;
  const clientOrders = state.orders.filter((o) => o.clientId === selectedId).sort((a, b) => b.createdAt.localeCompare(a.createdAt));

  useEffect(() => {
    msgsRef.current?.scrollTo({ top: msgsRef.current.scrollHeight });
  }, [ticket?.messages.length, selectedId]);

  const [sending, setSending] = useState(false);
  async function send() {
    if (!ticket || !text.trim() || sending) return;
    setSending(true);
    if (await dispatch({ type: 'sendMessage', ticketId: ticket.id, text: text.trim() })) setText('');
    setSending(false);
  }

  return (
    <div className="-mx-4 -my-4 flex h-[calc(100vh-6rem)] gap-4">
      {/* list */}
      <div className="flex w-72 shrink-0 flex-col overflow-hidden rounded-3xl border bg-card">
        <div className="flex flex-col gap-3 px-5 pt-6 pb-4">
          <h1 className="text-2xl font-semibold tracking-tight">Клієнти</h1>
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Знайти клієнта" aria-label="Знайти клієнта" />
          <FilterTabs value={tab} onChange={setTab} tabs={[{ key: 'all', label: 'Усі' }, { key: 'open', label: 'Відкриті звернення', count: openCount }]} />
        </div>
        <div className="flex-1 overflow-auto px-3 pb-3">
          {rows.length === 0 && <Empty>{tab === 'open' ? 'Відкритих звернень немає.' : 'Нікого не знайдено.'}</Empty>}
          {rows.map(({ c, ticket: t }) => {
            const o = t?.orderId ? order(t.orderId) : undefined;
            const unread = t?.status === 'open' && t.messages.at(-1)?.from === 'client';
            return (
              <button key={c.id} onClick={() => setParams({ client: c.id })} className={cn('mb-1 flex w-full items-center gap-3 rounded-2xl px-3 py-3 text-left transition-colors hover:bg-secondary', c.id === selectedId && 'bg-secondary')}>
                <Avatar name={c.name} className={c.id === selectedId ? 'bg-background' : undefined} />
                <div className="min-w-0 flex-1 text-sm">
                  <div className="flex justify-between gap-2 font-medium">
                    <span className="truncate">{c.name}</span>
                    <span className="tabular shrink-0 text-xs font-normal text-muted-foreground">{t ? (t.status === 'open' ? clock(t.updatedAt) : dayLabel(t.updatedAt, now).split(',')[0]) : ''}</span>
                  </div>
                  <div className="truncate text-muted-foreground">
                    {o && <span className="text-foreground">{o.number} · </span>}
                    {t ? t.summary : 'звернень немає'}
                  </div>
                </div>
                {unread && <span className="size-2 shrink-0 rounded-full bg-foreground" aria-label="Нове повідомлення" />}
              </button>
            );
          })}
        </div>
      </div>

      {/* thread */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden rounded-3xl border bg-card">
        {!sel || !ticket ? (
          <Empty>{sel ? 'Звернень від цього клієнта немає.' : 'Оберіть клієнта.'}</Empty>
        ) : (
          <>
            <div className="flex items-center gap-3 border-b px-6 py-4">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 font-semibold">
                  {sel.name}
                  {ticket.status === 'resolved' && <StatusDot tone="ok">Вирішено</StatusDot>}
                </div>
                {tOrder && <div className="truncate text-sm text-muted-foreground">Клієнт бачить «{clientStatus(tOrder.status, tOrder)}»</div>}
              </div>
              {tOrder && (
                <Button variant="outline" size="sm" onClick={() => navigate(`/orders/${tOrder.id}`)}>
                  Замовлення {tOrder.number}
                </Button>
              )}
              {ticket.status === 'open' && (
                <Button
                  size="sm"
                  onClick={() => {
                    dispatch({ type: 'resolveTicket', ticketId: ticket.id }, 'Звернення закрито');
                  }}
                >
                  Закрити звернення
                </Button>
              )}
            </div>
            <div ref={msgsRef} className="flex flex-1 flex-col gap-3 overflow-auto px-6 py-6">
              {ticket.messages.map((m) =>
                m.from === 'system' ? (
                  <div key={m.id} className="self-center rounded-full border px-3 py-1 text-xs text-muted-foreground">
                    {m.text}
                  </div>
                ) : (
                  <div key={m.id} className={cn('max-w-[64%] rounded-3xl px-4 py-2.5 text-sm leading-relaxed whitespace-pre-wrap', m.from === 'client' ? 'self-start rounded-bl-md bg-secondary' : 'self-end rounded-br-md bg-primary text-primary-foreground')}>
                    {m.text}
                    <div className={cn('mt-1 text-[11px]', m.from === 'client' ? 'text-muted-foreground' : 'text-primary-foreground/60')}>
                      {m.from === 'client' ? sel.name.split(' ')[0] : 'Підтримка Fitkit'} · {dayLabel(m.at, now)}
                    </div>
                  </div>
                ),
              )}
            </div>
            {ticket.status === 'open' && (
              <div className="flex flex-col gap-3 border-t px-6 py-4">
                <div className="flex flex-wrap gap-2">
                  {Object.entries(QUICK_REPLIES).map(([k, v]) => (
                    <Button key={k} size="sm" variant="secondary" onClick={() => setText(v)}>
                      {k}
                    </Button>
                  ))}
                </div>
                <form
                  className="flex items-end gap-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    send();
                  }}
                >
                  <Textarea
                    value={text}
                    rows={1}
                    onChange={(e) => setText(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !e.shiftKey) {
                        e.preventDefault();
                        send();
                      }
                    }}
                    placeholder="Відповідь від імені Підтримки Fitkit. Enter — надіслати"
                    className="max-h-40 min-h-11"
                  />
                  <Button type="submit" size="lg" disabled={!text.trim() || sending}>
                    <SendIcon /> Надіслати
                  </Button>
                </form>
              </div>
            )}
          </>
        )}
      </div>

      {/* details */}
      {sel && (
        <div className="flex w-72 shrink-0 flex-col gap-6 overflow-auto rounded-3xl border bg-card px-5 py-6 text-sm">
          <Section title="Контакти">
            <Field label="Телефон" value={sel.phone} />
            {sel.telegram && <Field label="Telegram" value={sel.telegram} />}
          </Section>
          <Section title="Доставка">
            <Field label="Місто" value={sel.city} />
            <Field label="Нова Пошта" value={sel.branch} />
          </Section>
          <Section title="Вподобання">
            <div className="grid grid-cols-[88px_1fr] gap-y-1.5">
              <span className="text-muted-foreground">Розміри</span>
              {sel.sizes}
              <span className="text-muted-foreground">Магазини</span>
              {sel.stores}
              {sel.dislikes && (
                <>
                  <span className="text-muted-foreground">Не любить</span>
                  {sel.dislikes}
                </>
              )}
            </div>
            {sel.note && <p className="mt-2 text-muted-foreground">{sel.note}</p>}
          </Section>
          <Section title={`Замовлення · ${clientOrders.length}`}>
            {clientOrders.slice(0, 5).map((o) => (
              <button key={o.id} onClick={() => navigate(`/orders/${o.id}`)} className="flex items-center gap-3 rounded-2xl bg-secondary px-3 py-2 text-left hover:bg-accent">
                <Thumbs items={o.items} max={2} className="h-8 w-6" />
                <span className="tabular font-medium">{o.number}</span>
                <span className="flex-1" />
                <OrderStatus status={o.status} />
              </button>
            ))}
          </Section>
        </div>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="mb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</div>
      {children}
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-1 rounded-2xl bg-secondary px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className={value ? 'font-medium' : 'text-muted-foreground'}>{value || 'не вказано'}</div>
      </div>
      {value && <CopyButton value={value} label={`Копіювати: ${label.toLowerCase()}`} />}
    </div>
  );
}
