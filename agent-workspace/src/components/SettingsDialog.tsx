import { LogOutIcon, MoonIcon, SunIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useStore } from '@/store/AppStore';
import { useSettings, type Theme } from '@/store/Settings';

export function SettingsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { settings, update } = useSettings();
  const { me, logout } = useStore();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Налаштування</DialogTitle>
          <DialogDescription>Зберігаються в цьому браузері.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-4 rounded-2xl bg-secondary px-4 py-3.5">
            <div>
              <div className="text-sm font-medium">Тема</div>
              <div className="text-xs text-muted-foreground">Оформлення інтерфейсу</div>
            </div>
            <Tabs value={settings.theme} onValueChange={(v) => update({ theme: v as Theme })}>
              <TabsList className="bg-background">
                <TabsTrigger value="light" className="data-[state=active]:bg-secondary">
                  <SunIcon className="size-4" /> Світла
                </TabsTrigger>
                <TabsTrigger value="dark" className="data-[state=active]:bg-secondary">
                  <MoonIcon className="size-4" /> Темна
                </TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
          <div className="flex items-center justify-between gap-4 rounded-2xl bg-secondary px-4 py-3.5">
            <Label htmlFor="show-requests" className="flex flex-col items-start gap-0.5">
              <span>Вкладка «Заявки»</span>
              <span className="text-xs font-normal text-muted-foreground">Черга нових заявок і кнопка «Взяти наступну»</span>
            </Label>
            <Switch id="show-requests" checked={settings.showRequests} onCheckedChange={(v) => update({ showRequests: v })} />
          </div>
        </div>
        <div className="flex items-center justify-between gap-4 border-t pt-4">
          <div className="min-w-0 text-sm">
            <div className="truncate font-medium">{me.name}</div>
            <div className="truncate text-xs text-muted-foreground">{me.email}</div>
          </div>
          <Button variant="outline" onClick={() => logout()}>
            <LogOutIcon /> Вийти
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
