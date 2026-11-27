import type { Analytics, Employee, EmployeeDetail, EmployeeInput, EmployeeView } from './types';

/**
 * Client for the admin API (/admin/api on the Fitkit server). The session lives in
 * the fitkit_admin cookie (path /admin), so every request is same-origin with credentials.
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Called on any 401 so the app can drop back to the login screen. */
let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/admin/api${path}`, {
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
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    /* not JSON */
  }
  if (!res.ok) {
    if (res.status === 401 && path !== '/auth/login') onUnauthorized();
    const msg = (data as { error?: string } | undefined)?.error ?? (res.status >= 500 ? 'Сервер недоступний' : `Помилка ${res.status}`);
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  login: (login: string, password: string) => call<{ login: string }>('POST', '/auth/login', { login, password }),
  logout: () => call<void>('POST', '/auth/logout'),
  me: () => call<{ login: string }>('GET', '/me'),
  analytics: () => call<Analytics>('GET', '/analytics'),
  employees: () => call<EmployeeView[]>('GET', '/employees'),
  employee: (id: string) => call<EmployeeDetail>('GET', `/employees/${encodeURIComponent(id)}`),
  createEmployee: (input: EmployeeInput) => call<Employee>('POST', '/employees', input),
  updateEmployee: (id: string, input: Omit<EmployeeInput, 'password'>) =>
    call<Employee>('PATCH', `/employees/${encodeURIComponent(id)}`, input),
  setPassword: (id: string, password: string) => call<void>('PUT', `/employees/${encodeURIComponent(id)}/password`, { password }),
  deleteEmployee: (id: string) => call<void>('DELETE', `/employees/${encodeURIComponent(id)}`),
};
