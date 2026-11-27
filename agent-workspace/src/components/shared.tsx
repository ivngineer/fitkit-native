import { useState, type ReactNode } from 'react';
import { CheckIcon, CopyIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { initials } from '@/domain/format';
import { STATUS_LABEL } from '@/domain/rules';
import type { Item, OrderStatus } from '@/domain/types';
import { cn } from '@/lib/utils';

/** Garment photo (white background cut-outs). */
export function Photo({ src, className }: { src: string; className?: string }) {
  const url = src.includes('/') ? src : `/assets/photos/${src}.jpg`;
  return <img src={url} alt="" loading="lazy" className={cn('shrink-0 rounded-xl bg-white object-cover', className)} />;
}

export function Thumbs({ items, max = 3, className = 'h-10 w-8' }: { items: Pick<Item, 'photo' | 'id'>[]; max?: number; className?: string }) {
  return (
    <div className="flex">
      {items.slice(0, max).map((i, n) => (
        <Photo key={i.id + n} src={i.photo} className={cn(className, 'rounded-lg ring-2 ring-secondary', n > 0 && '-ml-2')} />
      ))}
    </div>
  );
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  return <div className={cn('grid size-8 shrink-0 place-items-center rounded-full bg-secondary text-xs font-semibold text-foreground', className)}>{initials(name)}</div>;
}

export type Tone = 'neutral' | 'info' | 'warn' | 'bad' | 'ok';
const DOT: Record<Tone, string> = {
  neutral: 'bg-muted-foreground',
  info: 'bg-foreground',
  warn: 'bg-warning',
  bad: 'bg-destructive',
  ok: 'bg-success',
};

/** Grey pill with a small coloured dot — the only colour in the UI. */
export function StatusDot({ tone = 'neutral', children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <Badge variant="secondary" className={cn('font-medium', className)}>
      <span className={cn('size-1.5 rounded-full', DOT[tone])} />
      {children}
    </Badge>
  );
}

const ORDER_TONE: Record<OrderStatus, Tone> = {
  new: 'info',
  in_progress: 'neutral',
  awaiting: 'warn',
  purchased: 'neutral',
  shipped: 'ok',
  delivered: 'ok',
  cancelled: 'neutral',
};

export function OrderStatus({ status, overdue }: { status: OrderStatus; overdue?: boolean }) {
  if (overdue) return <StatusDot tone="bad">Прострочено</StatusDot>;
  return <StatusDot tone={ORDER_TONE[status]}>{STATUS_LABEL[status]}</StatusDot>;
}

/** Tabs used as a filter: value + counts. */
export function FilterTabs<K extends string>({ value, onChange, tabs }: { value: K; onChange: (k: K) => void; tabs: { key: K; label: string; count?: number; alert?: boolean }[] }) {
  return (
    <Tabs value={value} onValueChange={(v) => onChange(v as K)}>
      <TabsList>
        {tabs.map((t) => (
          <TabsTrigger key={t.key} value={t.key}>
            {t.label}
            {t.count != null && <span className={cn('tabular text-xs', t.alert && t.count > 0 ? 'text-destructive' : 'text-muted-foreground')}>{t.count}</span>}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

/** Small copy-to-clipboard icon button with feedback. */
export function CopyButton({ value, label = 'Копіювати', className }: { value: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className={cn('text-muted-foreground hover:text-foreground', className)}
          aria-label={label}
          onClick={() => {
            navigator.clipboard?.writeText(value);
            setDone(true);
            setTimeout(() => setDone(false), 1200);
          }}
        >
          {done ? <CheckIcon /> : <CopyIcon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{done ? 'Скопійовано' : label}</TooltipContent>
    </Tooltip>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="px-6 py-12 text-center text-sm text-muted-foreground">{children}</div>;
}

export function Count({ children }: { children: ReactNode }) {
  return <span className="tabular font-medium text-muted-foreground">{children}</span>;
}
