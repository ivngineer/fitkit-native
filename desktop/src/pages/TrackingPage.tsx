import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Count, Empty, FilterTabs, Photo, Thumbs } from '@/components/shared';
import { dayLabel, shortDate } from '@/domain/format';
import { clientStatus } from '@/domain/rules';
import type { Shipment, TrackingEvent } from '@/domain/types';
import { cn } from '@/lib/utils';
import { useLookups, useStore } from '@/store/AppStore';

type Tab = 'all' | 'in' | 'warehouse' | 'out' | 'problems';
const SOURCE = { email: 'з листа магазину', manual: 'введено закупником', api: 'створено через API Нової Пошти' } as const;

export function TrackingPage() {
  const { state, now, moveOrder, dispatch } = useStore();
  const { client, order } = useLookups();
  const [params, setParams] = useSearchParams();
  const [tab, setTab] = useState<Tab>('all');
  const [edit, setEdit] = useState<Shipment | null>(null);

  const all = useMemo(
    () => [...state.shipments].sort((a, b) => Number(!!b.problem) - Number(!!a.problem) || (b.events.at(-1)?.at ?? '').localeCompare(a.events.at(-1)?.at ?? '')),
    [state.shipments],
  );
  const filt: Record<Tab, (s: Shipment) => boolean> = {
    all: () => true,
    in: (s) => s.leg === 'in' && !s.atWarehouse,
    warehouse: (s) => s.leg === 'in' && !!s.atWarehouse && order(s.orderId)?.status === 'purchased',
    out: (s) => s.leg === 'out',
    problems: (s) => !!s.problem,
  };
  const rows = all.filter(filt[tab]);
  const count = (t: Tab) => all.filter(filt[t]).length;

  const selOrderId = params.get('order') ?? rows[0]?.orderId;
  const selOrder = selOrderId ? order(selOrderId) : undefined;
  const legIn = state.shipments.filter((s) => s.orderId === selOrderId && s.leg === 'in');
  const legOut = state.shipments.find((s) => s.orderId === selOrderId && s.leg === 'out');
  const ready = selOrder?.status === 'purchased' && legIn.length > 0 && legIn.every((s) => s.atWarehouse);

  return (
    <>
      <h1 className="text-2xl font-semibold tracking-tight">
        Відстеження <Count>{all.length}</Count>
      </h1>
      <FilterTabs
        value={tab}
        onChange={setTab}
        tabs={[
          { key: 'all', label: 'Усі', count: count('all') },
          { key: 'in', label: 'Магазин → склад', count: count('in') },
          { key: 'warehouse', label: 'На складі', count: count('warehouse') },
          { key: 'out', label: 'Склад → клієнт', count: count('out') },
          { key: 'problems', label: 'Проблеми', count: count('problems'), alert: true },
        ]}
      />
      <div className="grid grid-cols-[minmax(0,1fr)_340px] items-start gap-6">
        <Card className="gap-0 overflow-hidden py-0">
          {rows.length === 0 ? (
            <Empty>Посилок немає.</Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-6">Замовлення</TableHead>
                  <TableHead>Етап</TableHead>
                  <TableHead>Перевізник · ТТН</TableHead>
                  <TableHead className="pr-6">Остання подія</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((s) => {
                  const o = order(s.orderId)!;
                  const ev = s.events.at(-1);
                  return (
                    <TableRow key={s.id} data-state={s.orderId === selOrderId ? 'selected' : undefined} className="cursor-pointer hover:bg-secondary/50" onClick={() => setParams({ order: s.orderId })}>
                      <TableCell className="pl-6">
                        <div className="flex items-center gap-3">
                          <div className="w-16">
                            <Thumbs items={o.items} max={2} />
                          </div>
                          <span className="tabular font-semibold">{o.number}</span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="text-sm">{s.leg === 'in' ? 'Магазин → склад' : 'Склад → клієнт'}</div>
                        <div className="text-xs text-muted-foreground">{s.store ?? 'Fitkit'}</div>
                      </TableCell>
                      <TableCell>
                        <div className="tabular text-[13px]">{s.tracking ? `${s.carrier} · ${s.tracking}` : '—'}</div>
                        <div className="text-xs text-muted-foreground">{s.source ? SOURCE[s.source] : 'ТТН ще немає'}</div>
                      </TableCell>
                      <TableCell className="min-w-52 pr-6 whitespace-normal">
                        <div className={cn('flex items-center gap-2 text-sm', s.problem && 'text-destructive')}>
                          {s.problem && <span className="size-1.5 rounded-full bg-destructive" />}
                          {s.problem ?? ev?.title}
                        </div>
                        <div className="text-xs text-muted-foreground">{ev ? dayLabel(ev.at, now) : ''}</div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </Card>

        {selOrder && (
          <Card className="sticky top-24">
            <CardContent className="flex items-center gap-4">
              <Photo src={selOrder.items[0].photo} className="h-16 w-13" />
              <div className="min-w-0 flex-1">
                <Link to={`/orders/${selOrder.id}`} className="tabular text-lg font-semibold underline-offset-4 hover:underline">
                  {selOrder.number}
                </Link>
                <div className="truncate text-sm">
                  {selOrder.items[0].name} · {selOrder.items[0].size}
                  {selOrder.items.length > 1 ? ` +${selOrder.items.length - 1}` : ''}
                </div>
                <div className="text-sm text-muted-foreground">{client(selOrder.clientId).name}</div>
              </div>
            </CardContent>
            <CardContent className="flex flex-col gap-5">
              <Leg title="1 · Магазин → склад">
                {legIn.length === 0 && <div className="text-sm text-muted-foreground">Ще нічого не викуплено.</div>}
                {legIn.map((s) => (
                  <Timeline key={s.id} events={s.events} now={now}>
                    {!s.atWarehouse && (
                      <Step pending title={s.problem ?? 'Прибуде на склад'} sub={s.tracking ? 'оновлення від агрегатора трекінгу' : undefined}>
                        <div className="mt-2 flex flex-wrap gap-2">
                          {!s.tracking && (
                            <Button size="sm" variant="secondary" onClick={() => setEdit(s)}>
                              Додати ТТН
                            </Button>
                          )}
                          <Button size="sm" variant="outline" onClick={() => dispatch({ type: 'arrived', shipmentId: s.id }, `Посилку від ${s.store} прийнято на складі`)}>
                            Прийнято на складі
                          </Button>
                        </div>
                      </Step>
                    )}
                  </Timeline>
                ))}
              </Leg>
              <Leg title="2 · Склад → клієнт">
                {legOut ? (
                  <Timeline events={legOut.events} now={now} />
                ) : (
                  <Timeline events={[]} now={now}>
                    <Step pending title="Ще не відправлено" sub="ТТН Нової Пошти створить програма" />
                  </Timeline>
                )}
              </Leg>
              <div className="rounded-2xl bg-secondary px-4 py-3">
                <div className="text-xs font-medium tracking-wide text-muted-foreground uppercase">Клієнт бачить</div>
                <div className="mt-1 text-sm font-medium">
                  «{clientStatus(selOrder.status, selOrder)}»
                  {legOut ? ` · Нова Пошта ${legOut.tracking}` : ` · прибуде до ${shortDate(new Date(now + 2 * 86_400_000).toISOString())}`}
                </div>
              </div>
              {selOrder.status === 'purchased' && (
                <div className="flex flex-col gap-2">
                  <Button size="lg" className="w-full" disabled={!ready} onClick={() => moveOrder(selOrder.id, 'shipped')}>
                    Створити ТТН і відправити →
                  </Button>
                  {!ready && <p className="text-center text-xs text-muted-foreground">Стане доступно, коли всі посилки прибудуть на склад.</p>}
                </div>
              )}
            </CardContent>
          </Card>
        )}
      </div>
      <TrackingDialog shipment={edit} onClose={() => setEdit(null)} />
    </>
  );
}

function Leg({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</div>
      {children}
    </div>
  );
}

function Timeline({ events, now, children }: { events: TrackingEvent[]; now: number; children?: React.ReactNode }) {
  return (
    <ol className="relative flex flex-col gap-3 pl-6 before:absolute before:top-2 before:bottom-2 before:left-[5px] before:w-px before:bg-border">
      {events.map((e, i) => (
        <Step key={i} title={e.title} sub={`${e.detail ? e.detail + ' · ' : ''}${dayLabel(e.at, now)}`} />
      ))}
      {children}
    </ol>
  );
}

function Step({ title, sub, pending, children }: { title: string; sub?: string; pending?: boolean; children?: React.ReactNode }) {
  return (
    <li className="relative text-sm">
      <span className={cn('absolute top-1 -left-6 size-[11px] rounded-full border-2', pending ? 'border-muted-foreground bg-card' : 'border-foreground bg-foreground')} />
      <div className={cn('font-medium', pending && 'text-muted-foreground')}>{title}</div>
      {sub && <div className="text-xs text-muted-foreground">{sub}</div>}
      {children}
    </li>
  );
}

function TrackingDialog({ shipment, onClose }: { shipment: Shipment | null; onClose: () => void }) {
  const { dispatch } = useStore();
  const [carrier, setCarrier] = useState('');
  const [tracking, setTracking] = useState('');
  return (
    <Dialog
      open={!!shipment}
      onOpenChange={(v) => {
        if (!v) {
          onClose();
          setCarrier('');
          setTracking('');
        }
      }}
    >
      <DialogContent>
        <form
          className="grid gap-5"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!shipment) return;
            const ok = await dispatch({ type: 'setTracking', shipmentId: shipment.id, carrier: carrier.trim(), tracking: tracking.trim() }, 'ТТН додано — агрегатор відстежуватиме посилку');
            if (!ok) return;
            onClose();
            setCarrier('');
            setTracking('');
          }}
        >
          <DialogHeader>
            <DialogTitle>ТТН від {shipment?.store}</DialogTitle>
            <DialogDescription>Номер зазвичай приходить у листі магазину. Перевізника агрегатор визначить сам.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="trk">Номер ТТН</Label>
            <Input id="trk" autoFocus value={tracking} onChange={(e) => setTracking(e.target.value)} />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="car">Перевізник, необов’язково</Label>
            <Input id="car" value={carrier} onChange={(e) => setCarrier(e.target.value)} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Скасувати
            </Button>
            <Button type="submit" disabled={!tracking.trim()}>
              Зберегти
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
