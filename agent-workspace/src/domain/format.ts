export const uah = (n: number) => Math.round(n).toLocaleString('uk-UA') + ' ₴';

/** Ukrainian plural: plural(3, ['товар', 'товари', 'товарів']) → 'товари'. */
export function plural(n: number, [one, few, many]: [string, string, string]): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

export const itemsCount = (n: number) => `${n} ${plural(n, ['товар', 'товари', 'товарів'])}`;

export const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((w) => w[0])
    .join('')
    .slice(0, 2)
    .toUpperCase();

const MIN = 60_000;

/** text: “ще 1 год 20 хв”, “−42 хв” (overdue); short: “1 год 20 хв”. */
export function timeLeft(dueAt: string, now: number): { text: string; short: string; tone: '' | 'org' | 'red' } {
  const diff = Math.round((new Date(dueAt).getTime() - now) / MIN);
  const fmt = (m: number) => {
    const a = Math.abs(m);
    if (a < 60) return `${a} хв`;
    const h = Math.floor(a / 60);
    const r = a % 60;
    if (h >= 24) return `${Math.floor(h / 24)} д ${h % 24} год`;
    return r ? `${h} год ${r} хв` : `${h} год`;
  };
  if (diff < 0) return { text: `−${fmt(diff)}`, short: `−${fmt(diff)}`, tone: 'red' };
  return { text: `ще ${fmt(diff)}`, short: fmt(diff), tone: diff <= 15 ? 'org' : '' };
}

export function clock(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString('uk-UA', { hour: '2-digit', minute: '2-digit' });
}

export function dayLabel(iso: string, now: number): string {
  const d = new Date(iso);
  const n = new Date(now);
  const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString();
  if (sameDay(d, n)) return `сьогодні, ${clock(iso)}`;
  const y = new Date(now - 86_400_000);
  if (sameDay(d, y)) return `вчора, ${clock(iso)}`;
  return shortDate(iso) + ', ' + clock(iso);
}

export function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString('uk-UA', { day: 'numeric', month: 'long' });
}
