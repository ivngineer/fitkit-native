import type { Client, Employee, Id, Item, Order, OrderStatus, Shipment, Ticket } from '@/domain/types';

/**
 * HTTP client for the Go API (../api). In development Vite proxies /api to :8080,
 * so requests are same-origin and the session cookie is sent automatically.
 * The API keeps money in kopiyky; the UI works in hryvnias — converted here.
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: 'include',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, 'Немає зв’язку з сервером');
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    /* not JSON — e.g. the proxy's error page */
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | undefined)?.error ?? (res.status >= 500 ? 'Сервер недоступний' : `Помилка ${res.status}`);
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

// ---------- money: kopiyky ⇄ hryvnias

const toUAH = (k?: number | null) => (k == null ? undefined : k / 100);
const toKop = (u: number) => Math.round(u * 100);

type ApiItem = Omit<Item, 'price' | 'storePrice'> & { price?: number; storePrice?: number };
type ApiOrder = Omit<Order, 'items' | 'paid' | 'fee'> & { items: ApiItem[]; paid?: number; fee: number };

function order(o: ApiOrder): Order {
  return {
    id: o.id,
    number: o.number,
    clientId: o.clientId,
    ownerId: o.ownerId,
    status: o.status,
    createdAt: o.createdAt,
    dueAt: o.dueAt,
    paid: toUAH(o.paid),
    fee: toUAH(o.fee) ?? 0,
    problem: o.problem,
    location: o.location,
    photo: o.photo,
    notes: o.notes ?? [],
    history: o.history ?? [],
    items: o.items.map((i) => ({ ...i, price: toUAH(i.price), storePrice: toUAH(i.storePrice) })),
  };
}

export interface Snapshot {
  me: Employee;
  employees: Employee[];
  clients: Client[];
  orders: Order[];
  shipments: Shipment[];
  tickets: Ticket[];
}

export type ItemAction = 'check' | 'sold_out' | 'store_price' | 'send_confirmation' | 'client_confirmed' | 'cancel';

export const api = {
  login: (email: string, password: string) => call<{ me: Employee }>('POST', '/api/auth/login', { email, password }),
  logout: () => call<void>('POST', '/api/auth/logout'),
  me: () => call<Employee>('GET', '/api/me'),

  async bootstrap(): Promise<Snapshot> {
    const s = await call<Omit<Snapshot, 'orders'> & { orders: ApiOrder[] }>('GET', '/api/bootstrap');
    return { ...s, orders: s.orders.map(order) };
  },
  order: async (id: Id) => order(await call<ApiOrder>('GET', `/api/orders/${id}`)),
  shipments: (orderId?: Id) => call<Shipment[]>('GET', `/api/shipments${orderId ? `?order=${orderId}` : ''}`),
  tickets: () => call<Ticket[]>('GET', '/api/tickets'),

  takeNext: async () => order(await call<ApiOrder>('POST', '/api/orders/take-next')),
  take: async (id: Id) => order(await call<ApiOrder>('POST', `/api/orders/${id}/take`)),
  setStatus: async (id: Id, status: OrderStatus) => order(await call<ApiOrder>('POST', `/api/orders/${id}/status`, { status })),
  item: async (id: Id, itemId: Id, action: ItemAction, priceUAH?: number) =>
    order(await call<ApiOrder>('PATCH', `/api/orders/${id}/items/${itemId}`, { action, ...(priceUAH != null ? { price: toKop(priceUAH) } : {}) })),
  setProblem: async (id: Id, problem?: string) => order(await call<ApiOrder>('PUT', `/api/orders/${id}/problem`, { problem: problem ?? null })),
  addNote: (id: Id, text: string) => call<void>('POST', `/api/orders/${id}/notes`, { text }),
  cancel: async (id: Id) => order(await call<ApiOrder>('POST', `/api/orders/${id}/cancel`)),
  ship: async (id: Id) => order(await call<ApiOrder>('POST', `/api/orders/${id}/ship`)),

  setTracking: (shipmentId: Id, tracking: string, carrier?: string) => call<void>('POST', `/api/shipments/${shipmentId}/tracking`, { tracking, carrier: carrier ?? '' }),
  arrived: (shipmentId: Id) => call<void>('POST', `/api/shipments/${shipmentId}/arrived`),

  reply: (ticketId: Id, text: string) => call<void>('POST', `/api/tickets/${ticketId}/messages`, { text }),
  resolve: (ticketId: Id) => call<void>('POST', `/api/tickets/${ticketId}/resolve`),
};
