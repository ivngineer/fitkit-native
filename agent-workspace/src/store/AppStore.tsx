import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast as sonner } from 'sonner';
import { api, ApiError, type Snapshot } from '@/api/client';
import { canMarkPurchased, STATUS_LABEL } from '@/domain/rules';
import type { Client, Employee, Id, Order, OrderStatus } from '@/domain/types';

/** Server data as the UI sees it (money already in hryvnias). */
export interface State extends Omit<Snapshot, 'me'> {
  me: Id;
}

/** Every action is one API call; the server re-checks all rules. */
export type Action =
  | { type: 'take'; orderId: Id }
  | { type: 'setStatus'; orderId: Id; status: OrderStatus }
  | { type: 'itemCheck'; orderId: Id; itemId: Id }
  | { type: 'itemSoldOut'; orderId: Id; itemId: Id }
  | { type: 'itemCancel'; orderId: Id; itemId: Id }
  | { type: 'itemStorePrice'; orderId: Id; itemId: Id; price: number }
  | { type: 'itemSendConfirm'; orderId: Id; itemId: Id }
  | { type: 'itemClientConfirmed'; orderId: Id; itemId: Id }
  | { type: 'setProblem'; orderId: Id; problem?: string }
  | { type: 'cancelOrder'; orderId: Id }
  | { type: 'addNote'; orderId: Id; text: string }
  | { type: 'ship'; orderId: Id }
  | { type: 'setTracking'; shipmentId: Id; carrier: string; tracking: string }
  | { type: 'arrived'; shipmentId: Id }
  | { type: 'sendMessage'; ticketId: Id; text: string }
  | { type: 'resolveTicket'; ticketId: Id };

type Phase = 'loading' | 'signedOut' | 'ready' | 'offline';

interface Ctx {
  state: State;
  me: Employee;
  now: number;
  toast: (text: string, tone?: 'ok' | 'bad') => void;
  /** Runs an action; shows okMsg on success or the server's message on failure. */
  dispatch: (a: Action, okMsg?: string) => Promise<boolean>;
  /** Status change with the same guards as the server, for instant feedback (card and board). */
  moveOrder: (orderId: Id, status: OrderStatus) => Promise<boolean>;
  takeNext: () => Promise<Order | null>;
  logout: () => Promise<void>;
}

const StoreCtx = createContext<Ctx | null>(null);
const AuthCtx = createContext<{ phase: Phase; login: (email: string, password: string) => Promise<string | null>; retry: () => void } | null>(null);

const ITEM_ACTION = {
  itemCheck: 'check',
  itemSoldOut: 'sold_out',
  itemCancel: 'cancel',
  itemSendConfirm: 'send_confirmation',
  itemClientConfirmed: 'client_confirmed',
} as const;

export function AppStoreProvider({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>('loading');
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [now, setNow] = useState(Date.now());

  const toast = useCallback((text: string, tone: 'ok' | 'bad' = 'ok') => (tone === 'bad' ? sonner.error(text) : sonner(text)), []);

  const load = useCallback(async () => {
    try {
      setSnap(await api.bootstrap());
      setPhase('ready');
    } catch (e) {
      setPhase(e instanceof ApiError && e.status === 401 ? 'signedOut' : 'offline');
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [load]);

  // ---- targeted refreshes

  const upsertOrder = useCallback(
    (o: Order) => {
      setSnap((s) => {
        if (!s) return s;
        const i = s.orders.findIndex((x) => x.id === o.id);
        const orders = i >= 0 ? s.orders.map((x) => (x.id === o.id ? o : x)) : [...s.orders, o];
        return { ...s, orders };
      });
      // a new client came with this order (webhook from the client app)
      setSnap((s) => {
        if (s && !s.clients.some((c) => c.id === o.clientId)) load();
        return s;
      });
    },
    [load],
  );
  const refreshOrder = useCallback(async (id: Id) => upsertOrder(await api.order(id)), [upsertOrder]);
  const refreshShipments = useCallback(async () => {
    const shipments = await api.shipments();
    setSnap((s) => (s ? { ...s, shipments } : s));
  }, []);
  const refreshTickets = useCallback(async () => {
    const tickets = await api.tickets();
    setSnap((s) => (s ? { ...s, tickets } : s));
  }, []);

  // ---- live updates (Server-Sent Events), coalesced per object

  const pending = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  useEffect(() => {
    if (phase !== 'ready') return;
    const es = new EventSource('/api/events', { withCredentials: true });
    const schedule = (key: string, fn: () => Promise<unknown>) => {
      clearTimeout(pending.current.get(key));
      pending.current.set(
        key,
        setTimeout(() => {
          pending.current.delete(key);
          fn().catch(() => {});
        }, 150),
      );
    };
    const on = (type: string, fn: (id: string) => void) => es.addEventListener(type, (e) => fn(JSON.parse((e as MessageEvent).data).id));
    on('order', (id) => schedule('order:' + id, () => refreshOrder(id)));
    on('shipment', () => schedule('shipments', refreshShipments));
    on('ticket', () => schedule('tickets', refreshTickets));
    // after a reconnect we may have missed events — reload everything
    let opened = false;
    es.onopen = () => {
      if (opened) load();
      opened = true;
    };
    return () => es.close();
  }, [phase, refreshOrder, refreshShipments, refreshTickets, load]);

  // ---- actions

  const snapRef = useRef(snap);
  snapRef.current = snap;

  const fail = useCallback(
    (e: unknown) => {
      if (e instanceof ApiError && e.status === 401) {
        setPhase('signedOut');
        return;
      }
      toast(e instanceof Error ? e.message : 'Щось пішло не так', 'bad');
    },
    [toast],
  );

  const dispatch = useCallback(
    async (a: Action, okMsg?: string) => {
      try {
        switch (a.type) {
          case 'take':
            upsertOrder(await api.take(a.orderId));
            break;
          case 'setStatus':
            upsertOrder(await api.setStatus(a.orderId, a.status));
            if (a.status === 'purchased') await refreshShipments();
            break;
          case 'itemStorePrice':
            upsertOrder(await api.item(a.orderId, a.itemId, 'store_price', a.price));
            break;
          case 'itemCheck':
          case 'itemSoldOut':
          case 'itemCancel':
          case 'itemSendConfirm':
          case 'itemClientConfirmed':
            upsertOrder(await api.item(a.orderId, a.itemId, ITEM_ACTION[a.type]));
            break;
          case 'setProblem':
            upsertOrder(await api.setProblem(a.orderId, a.problem));
            break;
          case 'cancelOrder':
            upsertOrder(await api.cancel(a.orderId));
            break;
          case 'addNote':
            await api.addNote(a.orderId, a.text);
            await refreshOrder(a.orderId);
            break;
          case 'ship':
            upsertOrder(await api.ship(a.orderId));
            await refreshShipments();
            break;
          case 'setTracking':
          case 'arrived': {
            if (a.type === 'setTracking') await api.setTracking(a.shipmentId, a.tracking, a.carrier);
            else await api.arrived(a.shipmentId);
            await refreshShipments();
            const sh = snapRef.current?.shipments.find((x) => x.id === a.shipmentId);
            if (sh) await refreshOrder(sh.orderId);
            break;
          }
          case 'sendMessage':
            await api.reply(a.ticketId, a.text);
            await refreshTickets();
            break;
          case 'resolveTicket':
            await api.resolve(a.ticketId);
            await refreshTickets();
            break;
        }
        if (okMsg) toast(okMsg);
        return true;
      } catch (e) {
        fail(e);
        return false;
      }
    },
    [upsertOrder, refreshOrder, refreshShipments, refreshTickets, toast, fail],
  );

  const moveOrder = useCallback(
    async (orderId: Id, status: OrderStatus) => {
      const s = snapRef.current;
      const o = s?.orders.find((x) => x.id === orderId);
      if (!s || !o || o.status === status) return false;
      if (status === 'purchased' && !canMarkPurchased(o)) {
        toast('Спершу розберіть кожен товар: перевірте, застосуйте правило або скасуйте.', 'bad');
        return false;
      }
      if (status === 'awaiting' && !o.items.some((i) => i.status === 'awaiting')) {
        toast('Спершу надішліть запит на підтвердження ціни з картки заявки.', 'bad');
        return false;
      }
      if (status === 'shipped') {
        const inbound = s.shipments.filter((sh) => sh.orderId === orderId && sh.leg === 'in');
        if (o.status !== 'purchased') return toast('Відправити можна лише викуплене замовлення.', 'bad'), false;
        if (!inbound.length || !inbound.every((sh) => sh.atWarehouse)) return toast('Спершу всі посилки мають прибути на склад.', 'bad'), false;
        return dispatch({ type: 'ship', orderId }, `${o.number} відправлено · ТТН Нової Пошти створено`);
      }
      return dispatch({ type: 'setStatus', orderId, status }, `${o.number} → ${STATUS_LABEL[status]}`);
    },
    [dispatch, toast],
  );

  const takeNext = useCallback(async () => {
    try {
      const o = await api.takeNext();
      upsertOrder(o);
      toast(`${o.number} тепер ваша`);
      return o;
    } catch (e) {
      fail(e);
      return null;
    }
  }, [upsertOrder, toast, fail]);

  const login = useCallback(
    async (email: string, password: string) => {
      try {
        await api.login(email, password);
        setPhase('loading');
        await load();
        return null;
      } catch (e) {
        return e instanceof Error ? e.message : 'Не вдалося увійти';
      }
    },
    [load],
  );

  const logout = useCallback(async () => {
    await api.logout().catch(() => {});
    setSnap(null);
    setPhase('signedOut');
  }, []);

  const auth = useMemo(() => ({ phase, login, retry: () => (setPhase('loading'), load()) }), [phase, login, load]);
  const value = useMemo<Ctx | null>(
    () => (snap ? { state: { ...snap, me: snap.me.id }, me: snap.me, now, toast, dispatch, moveOrder, takeNext, logout } : null),
    [snap, now, toast, dispatch, moveOrder, takeNext, logout],
  );

  return (
    <AuthCtx.Provider value={auth}>
      <StoreCtx.Provider value={phase === 'ready' ? value : null}>{children}</StoreCtx.Provider>
    </AuthCtx.Provider>
  );
}

export function useAuth() {
  const c = useContext(AuthCtx);
  if (!c) throw new Error('useAuth outside AppStoreProvider');
  return c;
}

export function useStore() {
  const c = useContext(StoreCtx);
  if (!c) throw new Error('useStore used before sign-in');
  return c;
}

const UNKNOWN_CLIENT: Client = { id: '', name: '—', phone: '', city: '', branch: '', sizes: '', stores: '' };

export function useLookups() {
  const { state } = useStore();
  return useMemo(
    () => ({
      client: (id: Id) => state.clients.find((c) => c.id === id) ?? UNKNOWN_CLIENT,
      employee: (id?: Id) => state.employees.find((e) => e.id === id),
      order: (id: Id) => state.orders.find((o) => o.id === id),
      orderByNumber: (num: string) => state.orders.find((o) => o.number === num),
    }),
    [state],
  );
}
