import { Toaster as Sonner, type ToasterProps } from 'sonner';

function Toaster(props: ToasterProps) {
  return (
    <Sonner
      theme="dark"
      position="bottom-center"
      toastOptions={{ classNames: { toast: '!rounded-2xl !border-border !bg-popover !text-popover-foreground' } }}
      {...props}
    />
  );
}

export { Toaster };
