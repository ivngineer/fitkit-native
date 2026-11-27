import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRightIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Count, Empty, FilterTabs, OrderStatus, Thumbs } from '@/components/shared';
import { itemsCount, timeLeft, uah } from '@/domain/format';
import { cn } from '@/lib/utils';
import { useStore } from '@/store/AppStore';

type Tab = 'all' | 'new' | 'overdue';

export function RequestsPage() {
  const { state, now, takeNext: takeNextApi } = useStore();
  const navigate = useNavigate();
  const [tab, setTab] = useState<Tab>('all');

  const requests = useMemo(() => state.orders.filter((o) => o.status === 'new').sort((a, b) => a.dueAt.localeCompare(b.dueAt)), [state.orders]);
  const isLate = (dueAt: string) => new Date(dueAt).getTime() < now;
  const overdue = requests.filter((o) => isLate(o.dueAt));
  const fresh = requests.filter((o) => !isLate(o.dueAt));
  const rows = tab === 'overdue' ? overdue : tab === 'new' ? fresh : requests;

  const [busy, setBusy] = useState(false);
  async function takeNext() {
    setBusy(true);
    const o = await takeNextApi();
    setBusy(false);
    if (o) navigate(`/orders/${o.id}`);
  }

  return (
    <>
      <div className="flex items-center gap-4">
        <h1 className="flex-1 text-2xl font-semibold tracking-tight">
          Заявки <Count>{requests.length}</Count>
        </h1>
        <Button size="lg" onClick={takeNext} disabled={!requests.length || busy}>
          Взяти наступну заявку <ArrowRightIcon />
        </Button>
      </div>
      <FilterTabs
        value={tab}
        onChange={setTab}
        tabs={[
          { key: 'all', label: 'Усі', count: requests.length },
          { key: 'new', label: 'Нові', count: fresh.length },
          { key: 'overdue', label: 'Прострочені', count: overdue.length, alert: true },
        ]}
      />
      <Card className="gap-0 overflow-hidden py-0">
        {rows.length === 0 ? (
          <Empty>Тут порожньо — черга чиста.</Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-6">Заявка</TableHead>
                <TableHead>Магазини</TableHead>
                <TableHead>Сума</TableHead>
                <TableHead>Статус</TableHead>
                <TableHead className="pr-6 text-right">Термін</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((o) => {
                const late = isLate(o.dueAt);
                const t = timeLeft(o.dueAt, now);
                return (
                  <TableRow key={o.id} className="cursor-pointer hover:bg-secondary/50" onClick={() => navigate(`/orders/${o.id}`)}>
                    <TableCell className="pl-6">
                      <div className="flex items-center gap-4">
                        <div className="w-20">
                          <Thumbs items={o.items} />
                        </div>
                        <div>
                          <div className="tabular text-[15px] font-semibold">{o.number}</div>
                          <div className="text-xs text-muted-foreground">{itemsCount(o.items.length)}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{[...new Set(o.items.map((i) => i.store))].join(', ')}</TableCell>
                    <TableCell className="tabular">{o.paid != null ? uah(o.paid) : <span className="text-muted-foreground">ціна не вказана</span>}</TableCell>
                    <TableCell>
                      <OrderStatus status={o.status} overdue={late} />
                    </TableCell>
                    <TableCell className={cn('tabular pr-6 text-right font-medium', late ? 'text-destructive' : t.tone === 'org' ? 'text-warning' : 'text-muted-foreground')}>{t.text}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Card>
    </>
  );
}
