import { Toaster as Sonner, type ToasterProps } from 'sonner';
import { useSettings } from '@/store/Settings';

function Toaster(props: ToasterProps) {
  const { settings } = useSettings();
  return (
    <Sonner
      theme={settings.theme}
      position="bottom-center"
      toastOptions={{ classNames: { toast: '!rounded-2xl !border-border !bg-popover !text-popover-foreground' } }}
      {...props}
    />
  );
}

export { Toaster };
