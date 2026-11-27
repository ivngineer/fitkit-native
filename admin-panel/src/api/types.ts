export type Role = 'buyer' | 'warehouse' | 'support';

export interface Employee {
  id: string;
  name: string;
  email: string;
  role: Role;
  isTeamLead: boolean;
}

export interface Stats {
  completed: number;
  inProgress: number;
  onTheWay: number;
  cancelled: number;
  complaints: number;
}

export interface EmployeeView extends Employee {
  stats: Stats;
}

export interface OrderBrief {
  id: string;
  number: string;
  status: OrderStatus;
  clientName: string;
  createdAt: string;
}

export interface Complaint {
  id: string;
  orderId: string;
  orderNumber: string;
  clientName: string;
  text: string;
  at: string;
}

export interface EmployeeDetail extends EmployeeView {
  orders: OrderBrief[];
  complaints: Complaint[];
}

export interface LeaderboardRow {
  employee: Employee;
  count: number;
}

export interface Analytics {
  totals: { employees: number; orders: number; delivered: number; inProgress: number; complaints: number };
  leaderboard: LeaderboardRow[];
  mostComplaints: LeaderboardRow[];
}

export interface EmployeeInput {
  name: string;
  email: string;
  role: Role;
  isTeamLead: boolean;
  password?: string;
}

export type OrderStatus = 'new' | 'in_progress' | 'awaiting' | 'purchased' | 'shipped' | 'delivered' | 'cancelled';

export const ROLE_LABEL: Record<Role, string> = {
  buyer: 'Закупник',
  warehouse: 'Склад',
  support: 'Підтримка',
};

export const STATUS_LABEL: Record<OrderStatus, string> = {
  new: 'Нова',
  in_progress: 'В роботі',
  awaiting: 'Чекає підтвердження',
  purchased: 'Викуплено',
  shipped: 'Відправлено',
  delivered: 'Доставлено',
  cancelled: 'Скасовано',
};
