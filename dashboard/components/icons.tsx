import { BookOpen, CreditCard, LayoutDashboard, Scale, Store, Users, Webhook, type LucideIcon } from "lucide-react";

const icons: Record<string, LucideIcon> = {
  overview: LayoutDashboard,
  payments: CreditCard,
  merchants: Store,
  ledger: BookOpen,
  reconciliation: Scale,
  webhooks: Webhook,
  team: Users,
};

export function NavIcon({ name, className = "" }: { name: string; className?: string }) {
  const Icon = icons[name] ?? LayoutDashboard;
  return <Icon size={16} strokeWidth={1.5} className={className} aria-hidden />;
}
