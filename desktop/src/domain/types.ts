export type Id = string;

/** Internal order status. The client never sees these — see `clientStatus()` in rules.ts. */
export type OrderStatus = 'new' | 'in_progress' | 'awaiting' | 'purchased' | 'shipped' | 'delivered' | 'cancelled';

/**
 * Item status inside an order.
 * - pending:  not checked yet
 * - checked:  matches the client's photo, ready to buy
 * - sold_out: size/colour gone (rule: cancel & refund or another store)
 * - price_up: store price is higher than what the client paid (rule depends on threshold)
 * - awaiting: price confirmation sent to the client
 * - bought:   purchased in the store
 * - cancelled: removed from the order, money refunded
 */
export type ItemStatus = 'pending' | 'checked' | 'sold_out' | 'price_up' | 'awaiting' | 'bought' | 'cancelled';

export interface Employee {
  id: Id;
  name: string;
  email?: string;
  role: 'buyer' | 'lead' | 'warehouse' | 'support';
}

export interface Client {
  id: Id;
  name: string;
  telegram?: string;
  phone: string;
  /** Nova Poshta delivery: city + branch (or postomat). */
  city: string;
  branch: string;
  sizes: string;
  stores: string;
  dislikes?: string;
  note?: string;
}

export interface Item {
  id: Id;
  photo: string;
  name: string;
  store: string;
  url: string;
  size: string;
  qty: number;
  /** Price the client paid, ₴. Undefined when the client did not know it. */
  price?: number;
  /** Actual store price found by the buyer, ₴. */
  storePrice?: number;
  comment?: string;
  /** Store price as listed (any currency), when there is no price in hryvnias. */
  listingPrice?: string;
  status: ItemStatus;
}

export interface TeamNote {
  id: Id;
  author: string;
  at: string; // ISO
  text: string;
}

export interface HistoryEvent {
  at: string; // ISO
  who: string;
  what: string;
}

export interface Order {
  id: Id;
  number: string;
  clientId: Id;
  ownerId?: Id;
  status: OrderStatus;
  createdAt: string; // ISO
  dueAt: string; // ISO
  items: Item[];
  /** What the client paid, ₴. */
  paid?: number;
  fee: number;
  /** Red flag shown on any status. */
  problem?: string;
  /** Short location line for purchased/shipped orders on the board. */
  location?: string;
  /** The look the client bought from (pin image in the Fitkit app). */
  photo?: string;
  notes: TeamNote[];
  history: HistoryEvent[];
}

export type Leg = 'in' | 'out';
export type TrackingSource = 'email' | 'manual' | 'api';

export interface TrackingEvent {
  at: string; // ISO
  title: string;
  detail?: string;
}

export interface Shipment {
  id: Id;
  orderId: Id;
  leg: Leg;
  store?: string;
  carrier?: string;
  tracking?: string;
  source?: TrackingSource;
  events: TrackingEvent[];
  problem?: string;
  atWarehouse?: boolean;
}

export interface Message {
  id: Id;
  from: 'client' | 'support' | 'system';
  text: string;
  at: string; // ISO
  draft?: boolean;
}

export interface Ticket {
  id: Id;
  clientId: Id;
  orderId?: Id;
  status: 'open' | 'resolved';
  summary: string;
  messages: Message[];
  updatedAt: string; // ISO
}
