"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { NavIcon } from "@/components/icons";
import { isActive, linkClass, navGroups } from "@/lib/nav";

export function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return <NavTree pathname={pathname} onNavigate={onNavigate} />;
}

/** pathname is null for the prerendered fallback (nothing highlighted). */
export function NavTree({ pathname, onNavigate }: { pathname: string | null; onNavigate?: () => void }) {
  const active = (href: string) => pathname !== null && isActive(pathname, href);
  return (
    <nav className="space-y-6" aria-label="Main">
      {navGroups.map((g, i) => (
        <div key={g.label ?? i}>
          {g.label && <div className="mb-2 px-3 text-caption uppercase text-warm-gray">{g.label}</div>}
          <ul className="space-y-0.5">
            {g.items.map((l) => {
              const on = active(l.href);
              return (
                <li key={l.href}>
                  <Link href={l.href} onClick={onNavigate} className={linkClass(on)} aria-current={on ? "page" : undefined}>
                    <NavIcon name={l.icon} className={on ? "text-ink" : "text-slate group-hover:text-ink"} />
                    <span className="flex-1">{l.label}</span>
                    {on && <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}
