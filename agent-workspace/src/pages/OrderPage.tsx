import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { AlertTriangleIcon, ArrowLeftIcon, CheckIcon, CircleCheckIcon, ClockIcon, ExternalLinkIcon, FlagIcon, HistoryIcon, MessageSquareTextIcon, SearchIcon, XIcon } from 'lucide-react';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { CopyButton, Empty, OrderStatus, Photo, StatusDot, type Tone } from '@/components/shared';
import { clock, dayLabel, shortDate, timeLeft, uah } from '@/domain/format';
import { canMarkPurchased, clientStatus, money, nextStep, ruleFor } from '@/domain/rules';
import type { Client, Item, ItemStatus, Order } from '@/domain/types';
import { cn } from '@/lib/utils';
import { useLookups, useStore } from '@/store/AppStore';

const ITEM_STATUS: Record<ItemStatus, [string, Tone]> = {
  pending: ['Перевірити', 'neutral'],
  checked: ['Перевірено', 'ok'],
  sold_out: ['Немає', 'bad'],
  price_up: ['Ціна зросла', 'warn'],
  awaiting: ['Чекаємо клієнта', 'warn'],
  bought: ['Викуплено', 'ok'],
  cancelled: ['Скасовано', 'neutral'],
};

export function OrderPage() {
  const { id = '' } = useParams();
  const { state, now, moveOrder, dispatch } = useStore();
  const { client, employee, order: getOrder } = useLookups();
  const navigate = useNavigate();
  const [priceItem, setPriceItem] = useState<Item | null>(null);
  const [problemOpen, setProblemOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [notesOpen, setNotesOpen] = useState(true);

  const o = getOrder(id);
  if (!o)
    return (
      <Empty>
        Замовлення не знайдено.{' '}
        <Link to="/requests" className="underline underline-offset-4">
          До заявок
        </Link>
      </Empty>
    );

  const c = client(o.clientId);
  const owner = employee(o.ownerId);
  const mine = o.ownerId === state.me;
  const m = money(o);
  const t = timeLeft(o.dueAt, now);
  const step = nextStep(o.status);
  const blocked = (o.status === 'in_progress' || o.status === 'awaiting') && !canMarkPurchased(o);
  const editable = (o.status === 'new' || o.status === 'in_progress' || o.status === 'awaiting') && (mine || o.status === 'new');
  const active = o.status !== 'cancelled' && o.status !== 'delivered';

  function main() {
    if (!step) return;
    if (o!.status === 'new') {
      return dispatch({ type: 'take', orderId: o!.id }, `${o!.number} тепер ваша`);
    }
    return moveOrder(o!.id, step.to);
  }

  return (
    <>
      <div className="flex items-center gap-4">
        <Button variant="secondary" size="icon" onClick={() => navigate(-1)} aria-label="Назад">
          <ArrowLeftIcon />
        </Button>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="tabular text-2xl font-semibold tracking-tight">{o.number}</h1>
            <OrderStatus status={o.status} />
            {o.problem && <StatusDot tone="bad">{o.problem}</StatusDot>}
          </div>
          <div className="mt-0.5 text-sm text-muted-foreground">
            {c.name} · отримано {shortDate(o.createdAt)}, {clock(o.createdAt)}
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={() => setHistoryOpen(true)}>
          <HistoryIcon /> Історія
        </Button>
      </div>

      <div className="grid grid-cols-[minmax(0,5fr)_minmax(0,7fr)] items-start gap-6">
        {/* Left — items and delivery (narrow) */}
        <div className="flex min-w-0 flex-col gap-6">
          <Card className="gap-3 p-3">
            <div className="flex items-center gap-3 px-3 pt-2">
              <div className="flex-1 text-lg font-semibold">
                Товари <span className="tabular font-medium text-muted-foreground">{o.items.length}</span>
              </div>
              {o.photo && (
                <a href={o.photo} target="_blank" rel="noreferrer" className="flex items-center gap-2 rounded-2xl px-2 py-1 text-sm text-muted-foreground hover:bg-secondary hover:text-foreground">
                  Образ клієнта
                  <Photo src={o.photo} className="h-12 w-9 rounded-lg" />
                </a>
              )}
            </div>
            {o.items.map((it) => (
              <ItemRow key={it.id} order={o} item={it} editable={editable} onPrice={() => setPriceItem(it)} />
            ))}
          </Card>
          <DeliveryCard order={o} client={c} />
        </div>

        {/* Right — order details (wide) */}
        <div className="sticky top-24 flex min-w-0 flex-col gap-6">
          <Card>
            <CardContent className="grid grid-cols-3 gap-3">
              <Tile label="Відповідальний" value={owner?.name ?? 'Не призначено'} />
              <Tile label="Термін" value={t.text} tone={t.tone === 'red' ? 'bad' : t.tone === 'org' ? 'warn' : undefined} icon={<ClockIcon className="size-4" />} />
              <Tile label="Клієнт бачить" value={clientStatus(o.status, o)} />
            </CardContent>

            <CardContent className="mx-6 flex flex-col gap-2.5 rounded-2xl bg-secondary px-5 py-4 text-[15px]">
              <Row label="Клієнт сплатив" value={m.priceKnown ? uah(m.paid) : '—'} />
              {m.refunds > 0 && <Row label="Повернення клієнту" value={`−${uah(m.refunds)}`} />}
              <Row label="Комісія Fitkit" value={uah(m.fee)} />
              {m.covered > 0 && <Row label="Fitkit доплачує" value={uah(m.covered)} />}
              <Separator className="my-2" />
              <div className="flex items-baseline justify-between">
                <span className="text-muted-foreground">До викупу в магазинах</span>
                <span className="tabular text-3xl font-semibold tracking-tight">{m.toBuy ? uah(m.toBuy) : '—'}</span>
              </div>
            </CardContent>

            <CardContent className="flex flex-col gap-3">
              {step && (o.status === 'new' || mine) && (
                <Button size="lg" className="w-full" onClick={main} disabled={blocked}>
                  {step.label}
                </Button>
              )}
              {blocked && <p className="-mt-1 text-center text-xs text-muted-foreground">Щоб продовжити, розберіть кожен товар: перевірте, застосуйте правило або скасуйте.</p>}
              {active && o.status !== 'new' && (
                <div className="grid grid-cols-2 gap-3">
                  <Button variant={o.problem ? 'destructive' : 'secondary'} onClick={() => setProblemOpen(true)}>
                    <FlagIcon /> {o.problem ? 'Проблема позначена' : 'Проблема'}
                  </Button>
                  <Button variant="secondary" onClick={() => setNotesOpen((v) => !v)} aria-expanded={notesOpen}>
                    <MessageSquareTextIcon /> Нотатки команди
                    {o.notes.length > 0 && <span className="tabular text-muted-foreground">{o.notes.length}</span>}
                  </Button>
                </div>
              )}
              {notesOpen && active && o.status !== 'new' && <Notes order={o} />}
              {editable && o.status !== 'new' && <CancelOrder order={o} paid={m.priceKnown ? uah(m.paid) : null} />}
            </CardContent>
          </Card>
        </div>
      </div>

      <PriceDialog order={o} item={priceItem} onClose={() => setPriceItem(null)} />
      <ProblemDialog order={o} open={problemOpen} onOpenChange={setProblemOpen} />
      <Dialog open={historyOpen} onOpenChange={setHistoryOpen}>
        <DialogContent className="max-h-[80vh] overflow-auto">
          <DialogHeader>
            <DialogTitle>{o.number} · історія</DialogTitle>
          </DialogHeader>
          <ol className="flex flex-col gap-4">
            {[...o.history].reverse().map((h, i) => (
              <li key={i} className="text-sm">
                <div className="text-xs text-muted-foreground">
                  {h.who} · {dayLabel(h.at, now)}
                </div>
                {h.what}
              </li>
            ))}
          </ol>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Tile({ label, value, tone, icon }: { label: string; value: string; tone?: 'bad' | 'warn'; icon?: React.ReactNode }) {
  return (
    <div className="rounded-2xl bg-secondary px-4 py-3.5">
      <div className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{label}</div>
      <div className={cn('mt-1.5 flex items-center gap-1.5 text-[15px] leading-snug font-semibold', tone === 'bad' && 'text-destructive', tone === 'warn' && 'text-warning')}>
        {icon}
        {value}
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between">
      <span className="text-muted-foreground">{label}</span>
      <span className="tabular font-medium">{value}</span>
    </div>
  );
}

function ItemRow({ order, item, editable, onPrice }: { order: Order; item: Item; editable: boolean; onPrice: () => void }) {
  const { dispatch } = useStore();
  const rule = ruleFor(item);
  const [label, tone] = ITEM_STATUS[item.status];
  const cancelled = item.status === 'cancelled';
  const d = (type: 'itemCheck' | 'itemSoldOut' | 'itemCancel' | 'itemSendConfirm' | 'itemClientConfirmed', msg: string) => {
    dispatch({ type, orderId: order.id, itemId: item.id }, msg);
  };
  const price =
    item.price == null ? (
      <span className="text-muted-foreground">{item.storePrice ? `${uah(item.storePrice)} у магазині` : item.listingPrice ? `${item.listingPrice} у магазині` : 'ціна невідома'}</span>
    ) : item.storePrice != null && item.storePrice !== item.price ? (
      <>
        <span className="text-muted-foreground line-through">{uah(item.price)}</span> {uah(item.storePrice)}
      </>
    ) : (
      uah(item.price)
    );

  return (
    <div className={cn('flex gap-4 rounded-2xl bg-secondary p-4', cancelled && 'opacity-50')}>
      <Photo src={item.photo} className="h-20 w-16" />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <div className="truncate font-semibold">{item.name}</div>
            <div className="tabular text-sm">
              <span className="text-muted-foreground">{item.store} · </span>
              {item.size} · {item.qty} шт · {price}
            </div>
          </div>
          <StatusDot tone={tone}>{label}</StatusDot>
        </div>
        {item.comment && <div className="text-sm text-muted-foreground italic">{item.comment}</div>}
        {cancelled && <div className="text-sm text-muted-foreground">Скасовано · {uah((item.price ?? 0) * item.qty)} повернуто клієнту</div>}

        {rule && !cancelled && (
          <div className="rounded-xl bg-card px-3.5 py-3 text-sm">
            <div className="flex items-center gap-2 font-medium">
              {rule.kind === 'covered' ? (
                <CircleCheckIcon className="size-4 text-muted-foreground" />
              ) : (
                <AlertTriangleIcon className={cn('size-4', rule.kind === 'sold_out' ? 'text-destructive' : 'text-warning')} />
              )}
              {rule.title}
            </div>
            <div className="mt-1 text-muted-foreground">{rule.detail}</div>
            {editable && rule.kind === 'sold_out' && (
              <div className="mt-3 flex flex-wrap gap-2">
                <Button size="sm" onClick={() => d('itemCancel', `${item.name}: скасовано, кошти повернуто`)}>
                  Скасувати й повернути
                </Button>
                <Button size="sm" variant="outline" asChild>
                  <a href={`https://www.google.com/search?q=${encodeURIComponent(item.name + ' ' + item.size + ' купити')}`} target="_blank" rel="noreferrer">
                    <SearchIcon /> Інші магазини
                  </a>
                </Button>
              </div>
            )}
            {editable && rule.kind === 'confirm' && item.status !== 'awaiting' && (
              <div className="mt-3 flex flex-wrap gap-2">
                <Button size="sm" onClick={() => d('itemSendConfirm', 'Запит на підтвердження ціни надіслано клієнту')}>
                  Надіслати запит
                </Button>
                <Button size="sm" variant="outline" onClick={() => d('itemCancel', `${item.name}: скасовано, кошти повернуто`)}>
                  Скасувати й повернути
                </Button>
              </div>
            )}
            {editable && item.status === 'awaiting' && (
              <div className="mt-3 flex flex-wrap gap-2">
                <Button size="sm" variant="secondary" onClick={() => d('itemClientConfirmed', 'Клієнт підтвердив — купуйте за новою ціною')}>
                  Клієнт підтвердив
                </Button>
                <Button size="sm" variant="outline" onClick={() => d('itemCancel', `${item.name}: скасовано, кошти повернуто`)}>
                  Скасувати й повернути
                </Button>
              </div>
            )}
          </div>
        )}

        {!cancelled && (
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" asChild>
              <a href={item.url} target="_blank" rel="noreferrer">
                Відкрити в магазині <ExternalLinkIcon />
              </a>
            </Button>
            {editable && item.status === 'pending' && (
              <>
                <Button size="sm" variant="secondary" onClick={() => d('itemCheck', `${item.name}: перевірено`)}>
                  <CheckIcon /> Збігається
                </Button>
                <Button size="sm" variant="secondary" onClick={() => d('itemSoldOut', `${item.name}: немає в наявності`)}>
                  <XIcon /> Немає
                </Button>
              </>
            )}
            {editable && item.status !== 'sold_out' && (
              <Button size="sm" variant="ghost" onClick={onPrice}>
                {item.storePrice ? 'Змінити ціну' : 'Ціна в магазині'}
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** Large, bold, copy-friendly delivery data for the Nova Poshta shipment. */
function DeliveryCard({ order, client }: { order: Order; client: Client }) {
  const { toast } = useStore();
  const items = order.items.filter((i) => i.status !== 'cancelled' && i.status !== 'sold_out');
  const itemsText = items.map((i) => `${i.name}, ${i.size}, ${i.qty} шт`).join('\n');
  const all = [client.city, client.branch, client.name, client.phone, '', itemsText].join('\n');
  const fields: [string, string][] = [
    ['Місто', client.city],
    ['Нова Пошта', client.branch],
    ['Отримувач', client.name],
    ['Телефон', client.phone],
  ];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Доставка</CardTitle>
        <CardAction>
          <Button size="sm" variant="secondary" onClick={() => {
              navigator.clipboard?.writeText(all);
              toast('Дані доставки скопійовано');
            }}>
            Копіювати все
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {fields.map(([label, value]) => (
          <div key={label} className="flex items-center gap-2 rounded-2xl bg-secondary px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-xs text-muted-foreground">{label}</div>
              {value ? <div className="text-lg leading-snug font-semibold select-all">{value}</div> : <div className="text-lg leading-snug text-muted-foreground">не вказано</div>}
            </div>
            {value && <CopyButton value={value} label={`Копіювати: ${label.toLowerCase()}`} />}
          </div>
        ))}
        <div className="flex items-start gap-2 rounded-2xl bg-secondary px-4 py-3">
          <div className="min-w-0 flex-1">
            <div className="text-xs text-muted-foreground">Позиції · {items.length}</div>
            <ul className="mt-0.5 flex flex-col gap-0.5">
              {items.map((i) => (
                <li key={i.id} className="text-lg leading-snug font-semibold">
                  {i.name}, {i.size}, {i.qty} шт
                </li>
              ))}
            </ul>
          </div>
          <CopyButton value={itemsText} label="Копіювати позиції" />
        </div>
      </CardContent>
    </Card>
  );
}

function Notes({ order }: { order: Order }) {
  const { dispatch, now } = useStore();
  const [text, setText] = useState('');
  return (
    <div className="flex flex-col gap-3 rounded-2xl bg-secondary p-4">
      {order.notes.length === 0 && <div className="text-sm text-muted-foreground">Нотаток поки немає.</div>}
      {order.notes.map((n) => (
        <div key={n.id} className="text-sm">
          <div className="text-xs text-muted-foreground">
            <span className="font-medium text-foreground">{n.author}</span> · {dayLabel(n.at, now)}
          </div>
          {n.text}
        </div>
      ))}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (!text.trim()) return;
          dispatch({ type: 'addNote', orderId: order.id, text: text.trim() });
          setText('');
        }}
      >
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="Нотатка для команди" className="bg-background" />
        <Button type="submit" variant="secondary" disabled={!text.trim()}>
          Додати
        </Button>
      </form>
    </div>
  );
}

function CancelOrder({ order, paid }: { order: Order; paid: string | null }) {
  const { dispatch } = useStore();
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button variant="ghost" size="sm" className="self-center text-muted-foreground">
          Скасувати замовлення
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Скасувати {order.number}?</AlertDialogTitle>
          <AlertDialogDescription>Клієнт отримає повне повернення {paid ?? 'сплаченої суми'} і сповіщення «Скасовано, кошти повернуто».</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Залишити</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              dispatch({ type: 'cancelOrder', orderId: order.id }, `${order.number} скасовано`);
            }}
          >
            Скасувати й повернути кошти
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function PriceDialog({ order, item, onClose }: { order: Order; item: Item | null; onClose: () => void }) {
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>{item && <PriceForm key={item.id} order={order} item={item} onClose={onClose} />}</DialogContent>
    </Dialog>
  );
}

function PriceForm({ order, item, onClose }: { order: Order; item: Item; onClose: () => void }) {
  const { dispatch } = useStore();
  const [v, setV] = useState(String(item.storePrice ?? item.price ?? ''));
  const n = Number(v.replace(/[^\d.]/g, ''));
  const preview = n > 0 ? ruleFor({ ...item, storePrice: n }) : null;
  return (
    <form
      className="grid gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (n > 0) {
          dispatch({ type: 'itemStorePrice', orderId: order.id, itemId: item.id, price: n });
          onClose();
        }
      }}
    >
      <DialogHeader>
        <DialogTitle>Ціна в магазині</DialogTitle>
        <DialogDescription>
          {item.name} · клієнт сплатив {item.price != null ? uah(item.price) : '—'} за одиницю
        </DialogDescription>
      </DialogHeader>
      <Input autoFocus inputMode="numeric" value={v} onChange={(e) => setV(e.target.value)} aria-label="Ціна, ₴" />
      {preview && (
        <div className="rounded-2xl bg-secondary px-3.5 py-3 text-sm">
          <div className="font-medium">{preview.title}</div>
          <div className="mt-1 text-muted-foreground">{preview.detail}</div>
        </div>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Скасувати
        </Button>
        <Button type="submit" disabled={!(n > 0)}>
          Зберегти
        </Button>
      </DialogFooter>
    </form>
  );
}

const PROBLEMS = ['Магазин скасував замовлення', 'Оплату відхилено', 'Прийшов не той товар', 'Посилку втрачено або пошкоджено', 'Клієнт не на зв’язку'];

function ProblemDialog({ order, open, onOpenChange }: { order: Order; open: boolean; onOpenChange: (v: boolean) => void }) {
  const { dispatch } = useStore();
  const [text, setText] = useState(order.problem ?? '');
  const save = (p?: string) => {
    dispatch({ type: 'setProblem', orderId: order.id, problem: p }, p ? 'Проблему позначено — старшого зміни сповіщено' : 'Проблему знято');
    onOpenChange(false);
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (v) setText(order.problem ?? '');
        onOpenChange(v);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Проблема із замовленням</DialogTitle>
          <DialogDescription>Замовлення отримає червону позначку, старший зміни побачить його у сповіщеннях.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-2">
          {PROBLEMS.map((p) => (
            <Button key={p} size="sm" variant={text === p ? 'default' : 'secondary'} onClick={() => setText(p)}>
              {p}
            </Button>
          ))}
        </div>
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="Або опишіть своїми словами" />
        <DialogFooter>
          {order.problem && (
            <Button variant="outline" onClick={() => save(undefined)}>
              Зняти проблему
            </Button>
          )}
          <Button disabled={!text.trim()} onClick={() => save(text.trim())}>
            Позначити
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

