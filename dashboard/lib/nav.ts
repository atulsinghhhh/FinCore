export type NavItem = { href: string; label: string; icon: string };
export type NavGroup = { label?: string; items: NavItem[] };

export const navGroups: NavGroup[] = [
  { items: [{ href: "/", label: "Overview", icon: "overview" }] },
  {
    label: "Payments",
    items: [
      { href: "/payments", label: "Payments", icon: "payments" },
      { href: "/merchants", label: "Merchants", icon: "merchants" },
    ],
  },
  {
    label: "Finance",
    items: [
      { href: "/ledger", label: "Ledger", icon: "ledger" },
      { href: "/settlements", label: "Reconciliation", icon: "reconciliation" },
    ],
  },
  { label: "Developers", items: [{ href: "/outbox", label: "Webhooks", icon: "webhooks" }] },
  { label: "Settings", items: [{ href: "/team", label: "Team", icon: "team" }] },
];

export const links = navGroups.flatMap((g) => g.items);

const titles: Record<string, string> = Object.fromEntries(links.map((l) => [l.href, l.label]));
titles["/search"] = "Search";

/** Breadcrumb trail for a pathname, e.g. /payments/abc → Payments › abc… */
export function crumbs(pathname: string): { href: string; label: string }[] {
  if (pathname === "/") return [{ href: "/", label: "Overview" }];
  const parts = pathname.split("/").filter(Boolean);
  return parts.map((p, i) => {
    const href = "/" + parts.slice(0, i + 1).join("/");
    return { href, label: titles[href] ?? (p.length > 12 ? p.slice(0, 8) + "…" : p) };
  });
}

export function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(href + "/");
}

/** Kept for the prerendered fallback while the active state streams in. */
export function linkClass(active: boolean) {
  return `group flex items-center gap-3 rounded-lg px-3 py-2 text-data transition-colors ${
    active ? "bg-white text-ink shadow-[0_0_0_1px_var(--c-mist)]" : "text-slate hover:bg-white/60 hover:text-ink"
  }`;
}
